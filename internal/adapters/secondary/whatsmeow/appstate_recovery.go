package whatsmeow

import (
	"context"
	"errors"
	"fmt"
	"time"

	waClient "go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/appstate"
)

// appStateRecoveryTimeout bounds the WHOLE peer-assisted recovery —
// send + wait + post-verification (issue #381 review round 2: the
// budget used to arm only after the send, leaving the send unbounded).
// Deliberately a constant, not configuration: the fallback is an
// edge-triggered repair behind an explicit operator `appstate.resync`
// call, not tunable runtime behavior. whatsmeow's recovery handler has
// no error event — it Warnf-and-returns on every failure path — so this
// budget is the only failure signal the caller gets, and the slog DEBUG
// output of the whatsmeow `appstate` module (already wired via
// NewSlogLogger) is the diagnosis surface for a phone that answered
// with something unusable.
const appStateRecoveryTimeout = 120 * time.Second

// requestPeerRecovery asks the account's primary device for an
// unencrypted copy of a diverged app-state collection and blocks until
// the recovery is completed AND verified, the context is cancelled, the
// adapter shuts down, or the budget expires. Called only from
// ResyncAppState's full-sync LTHash-mismatch branch, which already holds
// the per-collection in-flight exclusion.
//
// Contract (issue #381, design review rounds 1–2):
//   - completion arrives via handleWAEvent from
//     events.AppStateSyncComplete with the EXACT collection name and
//     Recovery=true; ordinary full-sync completions (Recovery=false)
//     never satisfy a waiter. whatsmeow exposes no request-id
//     correlation, so the collection-scoped single-flight exclusion in
//     the caller is the correlation primitive;
//   - a successful SendPeerMessage is acknowledgement, not recovery —
//     whatsmeow mutates the store BEFORE dispatching the completing
//     event (appstate.go handleAppStateRecovery), and every failure path
//     there Warnf-and-returns, so timeout is the only caller-visible
//     failure signal;
//   - success is STATE-based: after the event, an incremental
//     FetchAppState(name,false,false) queues behind upstream's own
//     appStateSyncLock, observes the settled store, and re-verifies the
//     LTHash during decode. Any error fails closed — the collection
//     stays unrestored and writes keep failing;
//   - the ONLY outbound send is SendPeerMessage — an own-JID protocol
//     message, not a chat message, no digest, no tombstone.
func (a *Adapter) requestPeerRecovery(ctx context.Context, patch appstate.WAPatchName) error {
	key := string(patch)

	// ONE budget for send + wait + verification. Cancelled early when
	// the adapter shuts down (clientCancel), so a repair never outlives
	// the daemon.
	ctx, cancel := context.WithTimeout(ctx, appStateRecoveryTimeout)
	defer cancel()
	shutdown := a.clientCtx.Done()
	go func() {
		select {
		case <-shutdown:
			cancel()
		case <-ctx.Done():
		}
	}()

	// Waiter registration. Buffered capacity 1 + non-blocking send: the
	// completion path never blocks and never cleans up, so a late
	// duplicate event cannot disturb anything; identity cleanup belongs
	// to this attempt's defer alone.
	a.recoveryMu.Lock()
	done := make(chan struct{}, 1)
	a.recoveryPending[key] = done
	a.recoveryMu.Unlock()
	defer func() {
		a.recoveryMu.Lock()
		delete(a.recoveryPending, key)
		a.recoveryMu.Unlock()
	}()

	if _, err := a.client.SendPeerMessage(ctx, waClient.BuildAppStateRecoveryRequest(patch)); err != nil {
		if ctx.Err() != nil {
			return fmt.Errorf("peer app-state recovery for %s did not complete within %s (budget covers send + wait + verification): %w", key, appStateRecoveryTimeout, ctx.Err())
		}
		return fmt.Errorf("peer app-state recovery request for %s failed to send: %v", key, err)
	}

	select {
	case <-done:
	case <-ctx.Done():
		if errors.Is(ctx.Err(), context.Canceled) {
			if a.clientCtx.Err() != nil {
				return fmt.Errorf("peer app-state recovery for %s aborted: adapter shutting down: %w", key, ctx.Err())
			}
			return fmt.Errorf("peer app-state recovery for %s cancelled: %w", key, ctx.Err())
		}
		return fmt.Errorf("peer app-state recovery for %s not completed within %s: request was delivered to the primary device but no completion arrived: %w", key, appStateRecoveryTimeout, ctx.Err())
	}

	// Post-completion verification (fail closed).
	if err := a.client.FetchAppState(ctx, patch, false, false); err != nil {
		return fmt.Errorf("peer app-state recovery for %s completed but post-verification failed; failing closed: %w", key, err)
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
