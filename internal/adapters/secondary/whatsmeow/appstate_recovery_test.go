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

	"go.uber.org/goleak"

	"github.com/yolo-labz/wa/v2/internal/domain"
)

// lthashFetchErr reproduces the live failure shape from issue #381
// (12/09/2026, regular_high v428): the SERVER's own snapshot fails local
// verification, wrapped through the same %w chain ResyncAppState builds.
func lthashFetchErr() error {
	return fmt.Errorf("appstate.resync: whatsmeow.ResyncAppState(regular_high, full=true): failed to decode app state regular_high patches: failed to verify snapshot: failed to verify patch v428: %w", appstate.ErrMismatchingLTHash)
}

// settleStore wires the fake so the full (detect) fetch fails with the
// diverged LTHash sentinel while the incremental post-completion
// verification succeeds AND advances the persisted version for each
// settled collection — i.e. the phone's recovery actually settled them.
func settleStore(fc *fakeWhatsmeowClient, settled map[string]uint64) {
	fc.FetchAppStateFunc = func(name appstate.WAPatchName, fullSync, _ bool) error {
		if fullSync {
			return lthashFetchErr()
		}
		fc.mu.Lock()
		if v, ok := settled[string(name)]; ok {
			fc.AppStateVersions[string(name)] = v
		}
		fc.mu.Unlock()
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

// seedVersions gives the collections persisted versions so the
// post-completion verification has a baseline to require an advance from.
func seedVersions(fc *fakeWhatsmeowClient) {
	fc.mu.Lock()
	fc.AppStateVersions["regular_high"] = 424
	fc.AppStateVersions["regular_low"] = 100
	fc.mu.Unlock()
}

// T1 — full=true + LTHash sentinel escalates to peer recovery and
// succeeds ONLY when the exact collection reports Recovery=true AND the
// state-based verification passes: incremental catch-up (false,false)
// ran and the persisted version advanced past the pre-recovery read. The
// wire request is a COMPANION_SYNCD_SNAPSHOT_FATAL_RECOVERY peer data
// operation, and NO chat message was sent (D8).
func TestResyncFullLTHashTriggersPeerRecovery(t *testing.T) {
	a, fc := resyncAdapter(t)
	seedVersions(fc)
	settleStore(fc, map[string]uint64{"regular_high": 500})

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
	version := fc.AppStateVersions["regular_high"]
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
	if version != 500 {
		t.Fatalf("version = %d, want advanced to 500", version)
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
// went out), and the exclusion is released: an immediate retry fails the
// same way instead of claiming an in-flight attempt.
func TestPeerRecoverySendFailureReportedAndDeregistered(t *testing.T) {
	a, fc := resyncAdapter(t)
	seedVersions(fc)
	fc.FetchAppStateErr = lthashFetchErr()
	fc.PeerMessageErr = errors.New("websocket write failed")

	err := a.ResyncAppState(context.Background(), "regular_high", true)
	if err == nil || !strings.Contains(err.Error(), "failed to send") {
		t.Fatalf("err = %v, want send-failure wording", err)
	}
	err = a.ResyncAppState(context.Background(), "regular_high", true)
	if err == nil || !strings.Contains(err.Error(), "failed to send") {
		t.Fatalf("retry err = %v, want send-failure again (exclusion was not released)", err)
	}
	if peerMessageCount(fc) != 2 {
		t.Fatalf("peer messages = %d, want 2", peerMessageCount(fc))
	}
}

// T5 — only the EXACT collection with Recovery=true satisfies a waiter;
// wrong-collection and Recovery=false completions are ignored.
func TestPeerRecoveryCompletionCorrelation(t *testing.T) {
	a, fc := resyncAdapter(t)
	seedVersions(fc)
	settleStore(fc, map[string]uint64{"regular_high": 500})

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
// timeout; the worker outlives the call only until it unwinds (the
// exclusion is released by the reaper), late/duplicate completions are
// dropped no-ops, and a fresh attempt then works. Virtual time.
func TestPeerRecoveryTimeoutDeregistersAndDropsLateEvents(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		a, fc := resyncAdapter(t)
		seedVersions(fc)
		settleStore(fc, map[string]uint64{"regular_high": 500})

		errCh := make(chan error, 1)
		go func() { errCh <- a.ResyncAppState(context.Background(), "regular_high", true) }()
		waitPeerRequest(t, fc, 1)
		synctest.Wait() // worker parked on the completion/deadline select

		err := <-errCh
		if !strings.Contains(err.Error(), "did not complete within") {
			t.Fatalf("err = %v, want budget-expiry wording", err)
		}

		// Late completion after deregistration: dropped no-op. Duplicate
		// completion for a delivered waiter: equally harmless.
		a.handleWAEvent(completion(appstate.WAPatchRegularHigh, true))
		a.handleWAEvent(completion(appstate.WAPatchRegularHigh, true))

		// The exclusion was released synchronously; a fresh attempt
		// completes directly.
		go func() { errCh <- a.ResyncAppState(context.Background(), "regular_high", true) }()
		waitPeerRequest(t, fc, 1)
		a.handleWAEvent(completion(appstate.WAPatchRegularHigh, true))
		if err := <-errCh; err != nil {
			t.Fatalf("fresh attempt after timeout did not complete: %v", err)
		}
		assertNoChatSends(t, fc)
	})
}

// T7 — caller cancellation mid-wait unwinds with the context error; the
// exclusion clears once the worker unwinds.
func TestPeerRecoveryContextCancellation(t *testing.T) {
	a, fc := resyncAdapter(t)
	seedVersions(fc)
	settleStore(fc, map[string]uint64{"regular_high": 500})

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
}

// T8 — per-collection in-flight exclusion (acquired before the fetch,
// full or incremental): a second same-collection call refuses BEFORE any
// fetch and makes ZERO extra fetches/peer messages, while a different
// collection proceeds independently. Silence holds throughout.
func TestPeerRecoverySingleFlightAndIndependence(t *testing.T) {
	a, fc := resyncAdapter(t)
	seedVersions(fc)
	settleStore(fc, map[string]uint64{"regular_high": 500, "regular_low": 150})

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
	seedVersions(fc)
	settleStore(fc, map[string]uint64{"regular_high": 500})

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

// T10 — post-completion verification fails closed on BOTH legs: an
// incremental catch-up that still hits the LTHash sentinel is a failed
// recovery (cause preserved), and success is never claimed.
func TestPeerRecoveryPostVerifyFailClosed(t *testing.T) {
	a, fc := resyncAdapter(t)
	seedVersions(fc)
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

// T10b — a completion event whose store did NOT advance fails closed:
// event-only success (a plausible event over an unchanged store) is
// never claimed.
func TestPeerRecoveryVersionNonAdvanceFailClosed(t *testing.T) {
	a, fc := resyncAdapter(t)
	seedVersions(fc)
	fc.FetchAppStateFunc = func(_ appstate.WAPatchName, fullSync, _ bool) error {
		if fullSync {
			return lthashFetchErr()
		}
		return nil // verify passes but the store version never advances
	}

	errCh := make(chan error, 1)
	go func() { errCh <- a.ResyncAppState(context.Background(), "regular_high", true) }()
	waitPeerRequest(t, fc, 1)
	a.handleWAEvent(completion(appstate.WAPatchRegularHigh, true))

	err := <-errCh
	if err == nil || !strings.Contains(err.Error(), "did not advance past") {
		t.Fatalf("err = %v, want version-non-advance failure", err)
	}
}

// T11 — a cancellation arriving DURING the post-completion verification
// propagates (fail closed, no success claim).
func TestPeerRecoveryVerifyCancelledFailsClosed(t *testing.T) {
	a, fc := resyncAdapter(t)
	seedVersions(fc)
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
	// The watchdog and the worker race on cancellation; either reports
	// it — both fail closed and neither claims success.
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
}

// T12 — adapter shutdown aborts an in-flight repair (no repair outlives
// the daemon).
func TestPeerRecoveryAbortsOnAdapterShutdown(t *testing.T) {
	a, fc := resyncAdapter(t)
	seedVersions(fc)
	settleStore(fc, map[string]uint64{"regular_high": 500})

	errCh := make(chan error, 1)
	go func() { errCh <- a.ResyncAppState(context.Background(), "regular_high", true) }()
	waitPeerRequest(t, fc, 1)

	a.clientCancel()

	err := <-errCh
	if !errors.Is(err, context.Canceled) || !strings.Contains(err.Error(), "adapter shutting down") {
		t.Fatalf("err = %v, want adapter-shutdown abort", err)
	}
}

// T13 — Close() during an in-flight repair JOINS the worker before
// returning: the shutdown abort reaches the caller and Close does not
// race the worker's teardown.
func TestPeerRecoveryCloseJoinsWorker(t *testing.T) {
	a, fc := resyncAdapter(t)
	seedVersions(fc)
	settleStore(fc, map[string]uint64{"regular_high": 500})

	errCh := make(chan error, 1)
	go func() { errCh <- a.ResyncAppState(context.Background(), "regular_high", true) }()
	waitPeerRequest(t, fc, 1)

	closeDone := make(chan error, 1)
	go func() { closeDone <- a.Close() }()

	err := <-errCh
	if !errors.Is(err, context.Canceled) || !strings.Contains(err.Error(), "adapter shutting down") {
		t.Fatalf("err = %v, want adapter-shutdown abort", err)
	}
	select {
	case cerr := <-closeDone:
		if cerr != nil {
			t.Fatalf("Close: %v", cerr)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Close did not join the recovery worker")
	}
}

// T13b — Close under spawn contention: racers on DISTINCT collections
// (so each legitimately reaches beginAppStateWork) start behind a
// barrier racing Close's setDraining. Every racer must return without
// success, Close must join every worker before storage teardown, the
// post-draining call must be refused, and nothing may leak.
func TestPeerRecoveryCloseUnderContention(t *testing.T) {
	a, fc := resyncAdapter(t)
	fc.PeerMessageHang = true

	const racers = 5
	// Real collection names only — parsePatchName refuses anything else
	// before the exclusion/gate under test is ever reached.
	collections := []string{"critical_block", "critical_unblock_low", "regular_high", "regular", "regular_low"}
	versions := make(map[string]uint64, racers)
	for i, c := range collections {
		versions[c] = uint64(100 + i)
	}
	fc.mu.Lock()
	for c, v := range versions {
		fc.AppStateVersions[c] = v
	}
	fc.mu.Unlock()
	fc.FetchAppStateFunc = func(name appstate.WAPatchName, fullSync, _ bool) error {
		if fullSync {
			return lthashFetchErr()
		}
		return nil
	}

	start := make(chan struct{})
	errs := make(chan error, racers)
	for _, c := range collections {
		c := c
		go func() {
			<-start
			errs <- a.ResyncAppState(context.Background(), c, true)
		}()
	}
	closeDone := make(chan error, 1)
	go func() {
		<-start
		closeDone <- a.Close()
	}()
	close(start) // barrier: racers and Close race from here

	for i := 0; i < racers; i++ {
		select {
		case err := <-errs:
			if err == nil {
				t.Fatal("nil error under shutdown contention — success claimed during Close")
			}
			if !errors.Is(err, context.Canceled) &&
				!errors.Is(err, domain.ErrDisconnected) &&
				!strings.Contains(err.Error(), "adapter shutting down") &&
				!strings.Contains(err.Error(), "already in flight") {
				t.Fatalf("unexpected contender error: %v", err)
			}
		case <-time.After(2 * time.Second):
			t.Fatal("a contender never returned after Close")
		}
	}
	select {
	case cerr := <-closeDone:
		if cerr != nil {
			t.Fatalf("Close: %v", cerr)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Close did not return under contention")
	}

	// Post-draining: every app-state write path is refused, never
	// silently attempted.
	for _, c := range collections {
		err := a.ResyncAppState(context.Background(), c, true)
		if err == nil {
			t.Fatalf("post-draining resync of %s succeeded — draining gate failed", c)
		}
		if !errors.Is(err, context.Canceled) &&
			!errors.Is(err, domain.ErrDisconnected) &&
			!strings.Contains(err.Error(), "adapter shutting down") {
			t.Fatalf("post-draining resync of %s = %v, want a shutdown refusal", c, err)
		}
	}
	goleak.VerifyNone(t, leakFreeGoleakOptions()...)
}

// T14 — the budget covers the SEND itself: a transport that hangs until
// its context dies ends in budget-expiry wording, not an unbounded hang.
// Virtual time via testing/synctest.
func TestPeerRecoveryBudgetCoversSend(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		a, fc := resyncAdapter(t)
		seedVersions(fc)
		fc.FetchAppStateFunc = repairThenVerifyLike(fc)
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

// repairThenVerifyLike builds a settle-style FetchAppStateFunc without
// pinning a single collection (used by the hang test).
func repairThenVerifyLike(fc *fakeWhatsmeowClient) func(appstate.WAPatchName, bool, bool) error {
	return func(name appstate.WAPatchName, fullSync, _ bool) error {
		if fullSync {
			return lthashFetchErr()
		}
		fc.mu.Lock()
		fc.AppStateVersions[string(name)] = 500
		fc.mu.Unlock()
		return nil
	}
}
