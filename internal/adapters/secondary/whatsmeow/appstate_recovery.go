package whatsmeow

import (
	"context"
	"fmt"
	"time"

	waClient "go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/appstate"

	"github.com/yolo-labz/wa/v2/internal/domain"
)

// appStateRecoveryTimeout bounds how long ResyncAppState waits for the
// primary device to answer a peer-assisted recovery request (issue #381).
// Deliberately a constant, not configuration: the fallback is an
// edge-triggered repair behind an explicit operator `appstate.resync`
// call, not a tunable runtime behavior. whatsmeow's recovery handler has
// no error event — it Warnf-and-returns on every failure path — so this
// budget is the only failure signal the caller gets, and the slog DEBUG
// output of the whatsmeow `appstate` module (already wired via
// NewSlogLogger) is the diagnosis surface for a phone that answered with
// something unusable.
const appStateRecoveryTimeout = 120 * time.Second

// requestPeerRecovery asks the account's primary device for an
// unencrypted copy of a diverged app-state collection and blocks until
// the recovery completes, the context is cancelled, or the budget
// expires. Called only from ResyncAppState's full-sync LTHash-mismatch
// branch (contract D1).
//
// Contract (issue #381, spec conversation 13/09/2026):
//   - completion arrives via handleWAEvent from
//     events.AppStateSyncComplete with the EXACT collection name and
//     Recovery=true; ordinary full-sync completions (Recovery=false)
//     never satisfy a waiter (D3);
//   - a successful SendPeerMessage is acknowledgement, not recovery
//     (D2) — whatsmeow mutates the store BEFORE dispatching the
//     completion event (appstate.go handleAppStateRecovery), so receipt
//     of the event implies the collection is rebuilt and no post-event
//     store write happens here (D4);
//   - same-collection exclusion lives in ResyncAppState (acquired
//     before every explicit fetch); this function never refuses — the
//     caller guarantees single-flight, so completion attribution stays
//     unambiguous;
//   - the only outbound send is SendPeerMessage — an own-JID protocol
//     message, not a chat message, no digest, no tombstone (D8).
func (a *Adapter) requestPeerRecovery(ctx context.Context, patch appstate.WAPatchName) error {
	key := string(patch)

	// The caller (ResyncAppState) already holds the per-collection
	// in-flight exclusion for this collection; this function only
	// registers the completion waiter and is reached exclusively from
	// the full-sync LTHash fallback branch.
	a.recoveryMu.Lock()
	done := make(chan struct{})
	a.recoveryPending[key] = done
	a.recoveryMu.Unlock()
	defer func() {
		a.recoveryMu.Lock()
		delete(a.recoveryPending, key)
		a.recoveryMu.Unlock()
	}()

	if _, err := a.client.SendPeerMessage(ctx, waClient.BuildAppStateRecoveryRequest(patch)); err != nil {
		return fmt.Errorf("%w: recovery request for %s: %v", domain.ErrPeerRecoverySendFailed, key, err)
	}

	timer := time.NewTimer(appStateRecoveryTimeout)
	defer timer.Stop()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return fmt.Errorf("peer recovery for %s cancelled: %w", key, ctx.Err())
	case <-timer.C:
		return fmt.Errorf("%w: recovery request for %s was delivered to the primary device but no completion arrived within %s", domain.ErrPeerRecoveryTimeout, key, appStateRecoveryTimeout)
	}
}

// completePeerRecovery releases the waiter for a completed peer-assisted
// recovery. Safe to call for any AppStateSyncComplete: Recovery=false
// (ordinary full syncs) is ignored, a name with no waiter is a no-op, and
// the pending entry is removed under the mutex before the channel is
// closed so a late duplicate event cannot double-close (D3/D5).
func (a *Adapter) completePeerRecovery(name appstate.WAPatchName, recovery bool) {
	if !recovery {
		return
	}
	a.recoveryMu.Lock()
	ch, ok := a.recoveryPending[string(name)]
	if ok {
		delete(a.recoveryPending, string(name))
	}
	a.recoveryMu.Unlock()
	if ok {
		close(ch)
	}
}
