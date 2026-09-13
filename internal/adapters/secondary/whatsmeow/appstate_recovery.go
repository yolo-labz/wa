package whatsmeow

import (
	"context"
	"errors"
	"fmt"
	"time"

	waClient "go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/appstate"
)

// appStateRecoveryTimeout bounds how long ResyncAppState waits for a
// peer-assisted recovery (send + completion + verification) for a
// diverged app-state collection (issue #381). Deliberately a constant,
// not configuration: the fallback is an edge-triggered repair behind an
// explicit operator `appstate.resync` call, not tunable runtime
// behavior. whatsmeow's recovery handler has no error event — it
// Warnf-and-returns on every failure path — so this budget is the only
// failure signal the caller gets, and the slog DEBUG output of the
// whatsmeow `appstate` module (already wired via NewSlogLogger) is the
// diagnosis surface for a phone that answered with something unusable.
const appStateRecoveryTimeout = 120 * time.Second

// appStateVersionReader is an optional capability on the client seam.
// The real *whatsmeow.Client does not implement it: AppStateVersion
// falls back to reading the client's device store. The test fake
// implements it with an in-memory version map.
type appStateVersionReader interface {
	AppStateVersion(ctx context.Context, name string) (uint64, error)
}

// AppStateVersion reads the persisted version of one collection for the
// post-recovery verification (the collection must ADVANCE — whatsmeow's
// recovery handler skips responses that do not, and the failed full sync
// may have deleted the row entirely, so version 0 also fails).
func (a *Adapter) AppStateVersion(ctx context.Context, name string) (uint64, error) {
	if vr, ok := a.client.(appStateVersionReader); ok {
		return vr.AppStateVersion(ctx, name)
	}
	st := a.client.Store()
	if st == nil || st.AppState == nil {
		return 0, fmt.Errorf("app-state store unavailable")
	}
	v, _, err := st.AppState.GetAppStateVersion(ctx, name)
	return v, err
}

// requestPeerRecovery asks the account's primary device for an
// unencrypted copy of a diverged app-state collection and returns once
// the repair is completed AND verified, the caller cancels, the adapter
// shuts down, or the budget expires. Called only from ResyncAppState's
// full-sync LTHash-mismatch branch, and takes OWNERSHIP of
// releaseExclusion: the work runs in a joined worker goroutine that may
// outlive this call (stuck inside a non-context-aware upstream mutex —
// whatsmeow's messageSendLock/appStateSyncLock ignore contexts), and the
// per-collection exclusion must outlive the worker so two repairs can
// never overlap.
//
// Contract (issue #381, design review rounds 1–2):
//   - completion arrives via handleWAEvent from
//     events.AppStateSyncComplete with the EXACT collection name and
//     Recovery=true; ordinary full-sync completions (Recovery=false)
//     never satisfy a waiter. whatsmeow exposes no request-id
//     correlation, so the collection-scoped exclusion in the caller is
//     the correlation primitive;
//   - a successful SendPeerMessage is acknowledgement, not recovery —
//     whatsmeow mutates the store BEFORE dispatching the completing
//     event, and every failure path there Warnf-and-returns, so the
//     budget and the state checks below are the caller's only signals;
//   - success is STATE-based and fail-closed: after the event, an
//     incremental FetchAppState(name,false,false) applies any pending
//     server patches (verified on decode), and the persisted version
//     must have ADVANCED past the pre-recovery read — a plausible event
//     with an unchanged store is a failure, not a success;
//   - the ONLY outbound send is SendPeerMessage — an own-JID protocol
//     message, not a chat message, no digest, no tombstone.
func (a *Adapter) requestPeerRecovery(ctx context.Context, patch appstate.WAPatchName, releaseExclusion func()) error {
	key := string(patch)

	workCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	shutdown := a.clientCtx.Done()
	go func() {
		select {
		case <-shutdown:
			cancel()
		case <-workCtx.Done():
		}
	}()

	// Pre-recovery version. Required: without it the post-completion
	// check cannot prove the repair advanced state.
	preVersion, err := a.AppStateVersion(workCtx, key)
	if err != nil {
		releaseExclusion()
		return fmt.Errorf("peer app-state recovery for %s aborted: cannot read current app-state version (failing closed): %w", key, err)
	}

	// Waiter registration. Buffered capacity 1 + non-blocking send: the
	// completion path never blocks and never cleans up, so a late
	// duplicate event cannot disturb anything; identity cleanup belongs
	// to this attempt alone.
	a.recoveryMu.Lock()
	done := make(chan struct{}, 1)
	a.recoveryPending[key] = done
	a.recoveryMu.Unlock()

	workerExit := make(chan struct{})
	result := make(chan error, 1)
	a.appStateWG.Add(1)
	go func() {
		defer a.appStateWG.Done()
		defer close(workerExit) // actual exit — the reaper must not fire early
		result <- a.runPeerRecovery(workCtx, patch, preVersion, done)
	}()
	// Release the exclusion only when the worker has actually exited —
	// possibly long after this call returned an early error.
	go func() {
		<-workerExit
		releaseExclusion()
	}()

	timer := time.NewTimer(appStateRecoveryTimeout)
	defer timer.Stop()
	select {
	case err := <-result:
		<-workerExit
		return err
	case <-workCtx.Done():
		// Prefer the worker's actual outcome if it already unwound —
		// caller cancellation racing a verification failure must report
		// the failure, not a bare cancellation.
		if err, ok := drainResult(result, 50*time.Millisecond); ok {
			return err
		}
		if a.clientCtx.Err() != nil {
			return fmt.Errorf("peer app-state recovery for %s aborted: adapter shutting down: %w", key, workCtx.Err())
		}
		if errors.Is(workCtx.Err(), context.DeadlineExceeded) {
			return fmt.Errorf("peer app-state recovery for %s did not complete within its deadline: %w", key, workCtx.Err())
		}
		return fmt.Errorf("peer app-state recovery for %s cancelled: %w", key, workCtx.Err())
	case <-timer.C:
		// The watchdog bounds the caller even when an inner whatsmeow call
		// is stuck on a non-context-aware upstream mutex; the worker keeps
		// the collection excluded until it actually exits.
		return fmt.Errorf("peer app-state recovery for %s did not complete within %s: request was delivered to the primary device but no verified completion arrived", key, appStateRecoveryTimeout)
	}
}

// drainResult performs a non-blocking receive with an optional grace
// window, for error paths that race the worker's own outcome.
func drainResult(result <-chan error, grace time.Duration) (error, bool) {
	select {
	case err := <-result:
		return err, true
	default:
	}
	if grace > 0 {
		select {
		case err := <-result:
			return err, true
		case <-time.After(grace):
		}
	}
	return nil, false
}

// runPeerRecovery is the joined worker: send the peer request, wait for
// the exact-collection completion event, then verify by state.
func (a *Adapter) runPeerRecovery(ctx context.Context, patch appstate.WAPatchName, preVersion uint64, done <-chan struct{}) error {
	key := string(patch)

	if _, err := a.client.SendPeerMessage(ctx, waClient.BuildAppStateRecoveryRequest(patch)); err != nil {
		if ctx.Err() != nil {
			return fmt.Errorf("peer app-state recovery for %s aborted by shutdown/cancellation: %w", key, ctx.Err())
		}
		return fmt.Errorf("peer app-state recovery request for %s failed to send: %v", key, err)
	}

	select {
	case <-done:
	case <-ctx.Done():
		if errors.Is(ctx.Err(), context.Canceled) && a.clientCtx.Err() != nil {
			return fmt.Errorf("peer app-state recovery for %s aborted: adapter shutting down: %w", key, ctx.Err())
		}
		if errors.Is(ctx.Err(), context.Canceled) {
			return fmt.Errorf("peer app-state recovery for %s cancelled: %w", key, ctx.Err())
		}
		return fmt.Errorf("peer app-state recovery for %s not completed within %s: request was delivered to the primary device but no completion arrived: %w", key, appStateRecoveryTimeout, ctx.Err())
	}

	// Post-completion verification, fail closed. The incremental
	// catch-up applies any pending server patches (decoded + verified);
	// the version readback proves the collection actually advanced.
	if err := a.client.FetchAppState(ctx, patch, false, false); err != nil {
		return fmt.Errorf("peer app-state recovery for %s completed but post-verification failed; failing closed: %w", key, err)
	}
	postVersion, err := a.AppStateVersion(ctx, key)
	if err != nil {
		return fmt.Errorf("peer app-state recovery for %s post-verification failed: cannot read app-state version (failing closed): %w", key, err)
	}
	if postVersion == 0 || postVersion <= preVersion {
		return fmt.Errorf("peer app-state recovery for %s post-verification failed: version %d did not advance past %d (failing closed)", key, postVersion, preVersion)
	}
	return nil
}

// completePeerRecovery releases the waiter for a completed peer-assisted
// recovery. Safe to call for any AppStateSyncComplete: Recovery=false
// (ordinary full syncs) is ignored, a name with no waiter is a no-op,
// and the buffered non-blocking send makes a late duplicate event a
// dropped no-op — delivery never deletes or closes, so it cannot race
// the waiter's deregistration or double-release a newer attempt.
func (a *Adapter) completePeerRecovery(name appstate.WAPatchName, recovery bool) {
	if !recovery {
		return
	}
	a.recoveryMu.Lock()
	ch, ok := a.recoveryPending[string(name)]
	a.recoveryMu.Unlock()
	if ok {
		select {
		case ch <- struct{}{}:
		default:
		}
	}
}
