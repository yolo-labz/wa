package whatsmeow

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"go.mau.fi/whatsmeow/appstate"
	waE2E "go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/types/events"
)

// lthashFetchErr reproduces the live failure shape from issue #381
// (12/09/2026, regular_high v428): the SERVER's own snapshot fails local
// verification, wrapped through the same %w chain ResyncAppState builds.
func lthashFetchErr() error {
	return fmt.Errorf("appstate.resync: whatsmeow.ResyncAppState(regular_high, full=true): failed to decode app state regular_high patches: failed to verify snapshot: failed to verify patch v428: %w", appstate.ErrMismatchingLTHash)
}

// repairThenVerifyFunc models a collection that is diverged for the full
// fetch (the fallback trigger) and settled for the incremental
// post-completion verification.
func repairThenVerifyFunc() func(appstate.WAPatchName, bool, bool) error {
	return func(_ appstate.WAPatchName, fullSync, _ bool) error {
		if fullSync {
			return lthashFetchErr()
		}
		return nil
	}
}

func peerMessageCount(fc *fakeWhatsmeowClient) int {
	fc.mu.Lock()
	defer fc.mu.Unlock()
	return len(fc.PeerMessages)
}

// waitPeerRequest awaits n peer-recovery request signals (buffered
// rendezvous from the fake's SendPeerMessage) — no polling, per the
// repo's testing/synctest migration policy.
func waitPeerRequest(t *testing.T, fc *fakeWhatsmeowClient, n int) {
	t.Helper()
	for i := 0; i < n; i++ {
		select {
		case <-fc.PeerMessageSent:
		case <-time.After(2 * time.Second):
			t.Fatalf("timed out waiting for peer recovery request %d/%d; have %d", i+1, n, peerMessageCount(fc))
		}
	}
}

func assertNoChatSends(t *testing.T, fc *fakeWhatsmeowClient) {
	t.Helper()
	fc.mu.Lock()
	defer fc.mu.Unlock()
	if len(fc.SentMessages) != 0 {
		t.Fatalf("silence violated: %d chat SendMessage calls during peer recovery (contract D8)", len(fc.SentMessages))
	}
}

func completion(name appstate.WAPatchName, recovery bool) *events.AppStateSyncComplete {
	return &events.AppStateSyncComplete{Name: name, Version: 500, Recovery: recovery}
}

// T1 — full=true + LTHash sentinel escalates to peer recovery and
// succeeds when the exact collection reports Recovery=true AND the
// state-based post-verification (incremental catch-up, onlyIfNotSynced
// false) passes. The wire request is a
// COMPANION_SYNCD_SNAPSHOT_FATAL_RECOVERY peer data operation, and NO
// chat message was sent (D8).
func TestResyncFullLTHashTriggersPeerRecovery(t *testing.T) {
	a, fc := resyncAdapter(t)
	fc.FetchAppStateFunc = repairThenVerifyFunc()

	errCh := make(chan error, 1)
	go func() { errCh <- a.ResyncAppState(context.Background(), "regular_high", true) }()
	waitPeerRequest(t, fc, 1)
	a.handleWAEvent(completion(appstate.WAPatchRegularHigh, true))

	if err := <-errCh; err != nil {
		t.Fatalf("ResyncAppState with completed+verified recovery: %v", err)
	}
	fc.mu.Lock()
	msg := fc.PeerMessages[0].Msg
	calls := append([]recordedFetchAppState(nil), fc.FetchAppStateCalls...)
	fc.mu.Unlock()
	req := msg.GetProtocolMessage().GetPeerDataOperationRequestMessage()
	if req.GetPeerDataOperationRequestType() != waE2E.PeerDataOperationRequestType_COMPANION_SYNCD_SNAPSHOT_FATAL_RECOVERY {
		t.Fatalf("peer request type = %v, want COMPANION_SYNCD_SNAPSHOT_FATAL_RECOVERY", req.GetPeerDataOperationRequestType())
	}
	if got := req.GetSyncdCollectionFatalRecoveryRequest().GetCollectionName(); got != "regular_high" {
		t.Fatalf("recovery collection = %q, want regular_high", got)
	}
	if len(calls) != 2 {
		t.Fatalf("fetch calls = %d, want full-detect + incremental-verify", len(calls))
	}
	last := calls[len(calls)-1]
	if last.Full || last.OnlyIfNotSynced {
		t.Fatalf("post-verification fetch = full=%v onlyIfNotSynced=%v, want false,false", last.Full, last.OnlyIfNotSynced)
	}
	assertNoChatSends(t, fc)
}

// T2 — a non-LTHash failure on full=true surfaces the raw error and
// never escalates.
func TestResyncFullNonLTHashDoesNotEscalate(t *testing.T) {
	a, fc := resyncAdapter(t)
	fc.FetchAppStateErr = errors.New("server returned error updating app state (regular_high): conflict")

	err := a.ResyncAppState(context.Background(), "regular_high", true)
	if err == nil || strings.Contains(err.Error(), "peer recovery") {
		t.Fatalf("expected raw error without recovery fallback, got: %v", err)
	}
	if peerMessageCount(fc) != 0 {
		t.Fatal("peer recovery request sent for a non-LTHash failure")
	}
}

// T3 — full=false never escalates, even on the LTHash sentinel.
func TestResyncIncrementalLTHashDoesNotEscalate(t *testing.T) {
	a, fc := resyncAdapter(t)
	fc.FetchAppStateErr = lthashFetchErr()

	err := a.ResyncAppState(context.Background(), "regular_high", false)
	if err == nil || strings.Contains(err.Error(), "peer recovery") {
		t.Fatalf("expected raw error for incremental catch-up, got: %v", err)
	}
	if peerMessageCount(fc) != 0 {
		t.Fatal("peer recovery request sent for an incremental catch-up")
	}
}

// T4 — a send failure is reported as such ("failed to send" — nothing
// went out), and the attempt is fully deregistered: an immediate retry
// fails the same way instead of claiming an in-flight attempt.
func TestPeerRecoverySendFailureReportedAndDeregistered(t *testing.T) {
	a, fc := resyncAdapter(t)
	fc.FetchAppStateErr = lthashFetchErr()
	fc.PeerMessageErr = errors.New("websocket write failed")

	err := a.ResyncAppState(context.Background(), "regular_high", true)
	if err == nil || !strings.Contains(err.Error(), "failed to send") {
		t.Fatalf("err = %v, want send-failure wording", err)
	}
	err = a.ResyncAppState(context.Background(), "regular_high", true)
	if err == nil || !strings.Contains(err.Error(), "failed to send") {
		t.Fatalf("retry err = %v, want send-failure again (attempt was not deregistered)", err)
	}
	if peerMessageCount(fc) != 2 {
		t.Fatalf("peer messages = %d, want 2", peerMessageCount(fc))
	}
}

// T5 — only the EXACT collection with Recovery=true satisfies a waiter;
// wrong-collection and Recovery=false completions are ignored.
func TestPeerRecoveryCompletionCorrelation(t *testing.T) {
	a, fc := resyncAdapter(t)
	fc.FetchAppStateFunc = repairThenVerifyFunc()

	errCh := make(chan error, 1)
	go func() { errCh <- a.ResyncAppState(context.Background(), "regular_high", true) }()
	waitPeerRequest(t, fc, 1)

	// Wrong collection, Recovery=true: ignored.
	a.handleWAEvent(completion(appstate.WAPatchRegular, true))
	// Right collection, Recovery=false (ordinary full sync): ignored.
	a.handleWAEvent(completion(appstate.WAPatchRegularHigh, false))
	select {
	case err := <-errCh:
		t.Fatalf("waiter satisfied by a non-matching completion: %v", err)
	default:
	}

	a.handleWAEvent(completion(appstate.WAPatchRegularHigh, true))
	if err := <-errCh; err != nil {
		t.Fatalf("ResyncAppState after matching completion: %v", err)
	}
}

// T6 — under the budget a silent phone yields the delivered-but-unanswered
// timeout; the waiter is deregistered (the late completion is a dropped
// no-op, and a DUPLICATE completion after delivery is equally harmless),
// and a fresh attempt works. Virtual time via testing/synctest.
func TestPeerRecoveryTimeoutDeregistersAndDropsLateEvents(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		a, fc := resyncAdapter(t)
		fc.FetchAppStateFunc = repairThenVerifyFunc()

		errCh := make(chan error, 1)
		go func() { errCh <- a.ResyncAppState(context.Background(), "regular_high", true) }()
		waitPeerRequest(t, fc, 1)
		synctest.Wait() // worker parked on the completion/deadline select

		err := <-errCh
		if !strings.Contains(err.Error(), "not completed within") {
			t.Fatalf("err = %v, want budget-expiry wording", err)
		}
		if !strings.Contains(err.Error(), "delivered") {
			t.Fatalf("timeout error must distinguish delivered-but-silent, got: %v", err)
		}

		// Late completion after deregistration: dropped no-op.
		a.handleWAEvent(completion(appstate.WAPatchRegularHigh, true))
		// Duplicate completion for a delivered waiter would also be a
		// dropped no-op — prove the buffered send cannot panic or block.
		a.handleWAEvent(completion(appstate.WAPatchRegularHigh, true))

		// A fresh attempt is possible immediately (exclusion cleared).
		go func() { errCh <- a.ResyncAppState(context.Background(), "regular_high", true) }()
		waitPeerRequest(t, fc, 1)
		a.handleWAEvent(completion(appstate.WAPatchRegularHigh, true))
		if err := <-errCh; err != nil {
			t.Fatalf("fresh attempt after timeout did not complete: %v", err)
		}
		assertNoChatSends(t, fc)
	})
}

// T7 — caller cancellation mid-wait unwinds with the context error and
// leaves the exclusion cleared.
func TestPeerRecoveryContextCancellation(t *testing.T) {
	a, fc := resyncAdapter(t)
	fc.FetchAppStateFunc = repairThenVerifyFunc()

	ctx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error, 1)
	go func() { errCh <- a.ResyncAppState(ctx, "regular_high", true) }()
	waitPeerRequest(t, fc, 1)
	cancel()

	err := <-errCh
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
	go func() { errCh <- a.ResyncAppState(context.Background(), "regular_high", true) }()
	waitPeerRequest(t, fc, 1)
	a.handleWAEvent(completion(appstate.WAPatchRegularHigh, true))
	if err := <-errCh; err != nil {
		t.Fatalf("fresh attempt after cancellation did not complete: %v", err)
	}
}

// T8 — per-collection in-flight exclusion (acquired before the fetch,
// full or incremental): a second same-collection call refuses BEFORE any
// fetch and makes ZERO extra fetches/peer messages, while a different
// collection proceeds independently. Silence holds throughout.
func TestPeerRecoverySingleFlightAndIndependence(t *testing.T) {
	a, fc := resyncAdapter(t)
	fc.FetchAppStateFunc = repairThenVerifyFunc()

	errCh := make(chan error, 2)
	go func() { errCh <- a.ResyncAppState(context.Background(), "regular_high", true) }()
	waitPeerRequest(t, fc, 1)

	fc.mu.Lock()
	fetchesAfterFirst := len(fc.FetchAppStateCalls)
	fc.mu.Unlock()

	err := a.ResyncAppState(context.Background(), "regular_high", true)
	if err == nil || !strings.Contains(err.Error(), "already in flight") {
		t.Fatalf("second attempt err = %v, want 'already in flight'", err)
	}
	fc.mu.Lock()
	fetchesAfterSecond := len(fc.FetchAppStateCalls)
	fc.mu.Unlock()
	if fetchesAfterSecond != fetchesAfterFirst {
		t.Fatalf("refused second attempt made extra fetches: %d -> %d", fetchesAfterFirst, fetchesAfterSecond)
	}
	if peerMessageCount(fc) != 1 {
		t.Fatalf("peer messages = %d, want 1 (no extra request)", peerMessageCount(fc))
	}

	// A different collection runs its own sync+recovery to completion.
	go func() { errCh <- a.ResyncAppState(context.Background(), "regular_low", true) }()
	waitPeerRequest(t, fc, 1)
	a.handleWAEvent(completion(appstate.WAPatchRegularLow, true))
	if err := <-errCh; err != nil {
		t.Fatalf("independent collection recovery: %v", err)
	}

	// The original waiter is untouched by the other collection's events.
	select {
	case err := <-errCh:
		t.Fatalf("regular_high waiter completed via cross-collection event: %v", err)
	default:
	}
	a.handleWAEvent(completion(appstate.WAPatchRegularHigh, true))
	if err := <-errCh; err != nil {
		t.Fatalf("regular_high recovery: %v", err)
	}
	assertNoChatSends(t, fc)
}

// T9 — the exclusion also covers incremental syncs: while a full repair
// with its fallback is in flight, an incremental same-collection call
// refuses instead of interleaving with the mid-repair store.
func TestResyncIncrementalRefusedWhileRepairInFlight(t *testing.T) {
	a, fc := resyncAdapter(t)
	fc.FetchAppStateFunc = repairThenVerifyFunc()

	errCh := make(chan error, 1)
	go func() { errCh <- a.ResyncAppState(context.Background(), "regular_high", true) }()
	waitPeerRequest(t, fc, 1)

	err := a.ResyncAppState(context.Background(), "regular_high", false)
	if err == nil || !strings.Contains(err.Error(), "already in flight") {
		t.Fatalf("incremental-during-repair err = %v, want 'already in flight'", err)
	}

	a.handleWAEvent(completion(appstate.WAPatchRegularHigh, true))
	if err := <-errCh; err != nil {
		t.Fatalf("recovery after refused interleave: %v", err)
	}
}

// T10 — post-completion verification fails closed: when the incremental
// catch-up still hits the LTHash sentinel, the recovery is reported as
// failed (LTHash cause preserved through the wrap) and success is never
// claimed.
func TestPeerRecoveryPostVerifyFailClosed(t *testing.T) {
	a, fc := resyncAdapter(t)
	fc.FetchAppStateFunc = func(_ appstate.WAPatchName, fullSync, _ bool) error {
		return lthashFetchErr() // diverged for BOTH detect and verify
	}

	errCh := make(chan error, 1)
	go func() { errCh <- a.ResyncAppState(context.Background(), "regular_high", true) }()
	waitPeerRequest(t, fc, 1)
	a.handleWAEvent(completion(appstate.WAPatchRegularHigh, true))

	err := <-errCh
	if err == nil || !strings.Contains(err.Error(), "post-verification failed") {
		t.Fatalf("err = %v, want post-verification failure", err)
	}
	if !errors.Is(err, appstate.ErrMismatchingLTHash) {
		t.Fatalf("cause chain lost ErrMismatchingLTHash: %v", err)
	}
	assertNoChatSends(t, fc)
}

// T11 — a cancellation arriving DURING the post-completion verification
// propagates (fail closed, no success claim).
func TestPeerRecoveryVerifyCancelledFailsClosed(t *testing.T) {
	a, fc := resyncAdapter(t)
	ctx, cancel := context.WithCancel(context.Background())
	fc.FetchAppStateFunc = func(_ appstate.WAPatchName, fullSync, _ bool) error {
		if fullSync {
			return lthashFetchErr()
		}
		<-ctx.Done() // verification hangs until the caller cancels
		return ctx.Err()
	}

	errCh := make(chan error, 1)
	go func() { errCh <- a.ResyncAppState(ctx, "regular_high", true) }()
	waitPeerRequest(t, fc, 1)
	a.handleWAEvent(completion(appstate.WAPatchRegularHigh, true))
	cancel()

	err := <-errCh
	if !errors.Is(err, context.Canceled) || !strings.Contains(err.Error(), "post-verification failed") {
		t.Fatalf("err = %v, want cancelled post-verification failure", err)
	}
}

// T12 — adapter shutdown aborts an in-flight repair (no repair outlives
// the daemon).
func TestPeerRecoveryAbortsOnAdapterShutdown(t *testing.T) {
	a, fc := resyncAdapter(t)
	fc.FetchAppStateFunc = repairThenVerifyFunc()

	errCh := make(chan error, 1)
	go func() { errCh <- a.ResyncAppState(context.Background(), "regular_high", true) }()
	waitPeerRequest(t, fc, 1)

	a.clientCancel()

	err := <-errCh
	if !errors.Is(err, context.Canceled) || !strings.Contains(err.Error(), "adapter shutting down") {
		t.Fatalf("err = %v, want adapter-shutdown abort", err)
	}
}

// T13 — the budget covers the SEND itself: a transport that hangs until
// its context dies ends in budget-expiry wording, not an unbounded hang.
// Virtual time via testing/synctest.
func TestPeerRecoveryBudgetCoversSend(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		a, fc := resyncAdapter(t)
		fc.FetchAppStateFunc = repairThenVerifyFunc()
		fc.PeerMessageHang = true

		errCh := make(chan error, 1)
		go func() { errCh <- a.ResyncAppState(context.Background(), "regular_high", true) }()
		synctest.Wait() // worker parked inside the hanging send

		err := <-errCh
		if !strings.Contains(err.Error(), "did not complete within") {
			t.Fatalf("err = %v, want budget-expiry wording covering the send", err)
		}
	})
}
