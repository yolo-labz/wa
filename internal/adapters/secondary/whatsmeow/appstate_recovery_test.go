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

	"github.com/yolo-labz/wa/v2/internal/domain"
)

// lthashFetchErr reproduces the live failure shape from issue #381
// (12/09/2026, regular_high v428): the SERVER's own snapshot fails local
// verification, wrapped through the same %w chain ResyncAppState builds.
func lthashFetchErr() error {
	return fmt.Errorf("appstate.resync: whatsmeow.ResyncAppState(regular_high, full=true): failed to decode app state regular_high patches: failed to verify snapshot: failed to verify patch v428: %w", appstate.ErrMismatchingLTHash)
}

func peerMessageCount(fc *fakeWhatsmeowClient) int {
	fc.mu.Lock()
	defer fc.mu.Unlock()
	return len(fc.PeerMessages)
}

// waitPeerMessage polls until n peer messages were recorded. Real-time
// callers use it after spawning the resync goroutine; synctest bubbles
// advance virtual time through the sleeps.
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

// T1a — full=true + LTHash sentinel escalates to peer recovery and
// completes when the exact collection reports Recovery=true. The wire
// request is a COMPANION_SYNCD_SNAPSHOT_FATAL_RECOVERY peer data
// operation addressed to the primary device, and NO chat message was
// sent (D8).
func TestResyncFullLTHashTriggersPeerRecovery(t *testing.T) {
	a, fc := resyncAdapter(t)
	fc.FetchAppStateErr = lthashFetchErr()

	errCh := make(chan error, 1)
	go func() { errCh <- a.ResyncAppState(context.Background(), "regular_high", true) }()
	waitPeerRequest(t, fc, 1)
	a.handleWAEvent(completion(appstate.WAPatchRegularHigh, true))

	if err := <-errCh; err != nil {
		t.Fatalf("ResyncAppState with completed recovery: %v", err)
	}
	fc.mu.Lock()
	msg := fc.PeerMessages[0].Msg
	fc.mu.Unlock()
	req := msg.GetProtocolMessage().GetPeerDataOperationRequestMessage()
	if req.GetPeerDataOperationRequestType() != waE2E.PeerDataOperationRequestType_COMPANION_SYNCD_SNAPSHOT_FATAL_RECOVERY {
		t.Fatalf("peer request type = %v, want COMPANION_SYNCD_SNAPSHOT_FATAL_RECOVERY", req.GetPeerDataOperationRequestType())
	}
	if got := req.GetSyncdCollectionFatalRecoveryRequest().GetCollectionName(); got != "regular_high" {
		t.Fatalf("recovery collection = %q, want regular_high", got)
	}
	assertNoChatSends(t, fc)
}

// T1b — a non-LTHash failure on full=true surfaces the raw error and
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

// T1c — full=false never escalates, even on the LTHash sentinel.
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

// T2 — a send failure is typed ErrPeerRecoverySendFailed, and the
// attempt is fully deregistered: an immediate retry fails with SEND
// FAILED again, not ErrPeerRecoveryInProgress.
func TestPeerRecoverySendFailureTypedAndDeregistered(t *testing.T) {
	a, fc := resyncAdapter(t)
	fc.FetchAppStateErr = lthashFetchErr()
	fc.PeerMessageErr = errors.New("websocket write failed")

	err := a.ResyncAppState(context.Background(), "regular_high", true)
	if !errors.Is(err, domain.ErrPeerRecoverySendFailed) {
		t.Fatalf("err = %v, want ErrPeerRecoverySendFailed", err)
	}
	err = a.ResyncAppState(context.Background(), "regular_high", true)
	if !errors.Is(err, domain.ErrPeerRecoverySendFailed) {
		t.Fatalf("retry err = %v, want ErrPeerRecoverySendFailed again (attempt was not deregistered)", err)
	}
	if peerMessageCount(fc) != 2 {
		t.Fatalf("peer messages = %d, want 2", peerMessageCount(fc))
	}
}

// T4 — only the EXACT collection with Recovery=true satisfies a waiter;
// wrong-collection and Recovery=false completions are ignored (D3).
func TestPeerRecoveryCompletionCorrelation(t *testing.T) {
	a, fc := resyncAdapter(t)
	fc.FetchAppStateErr = lthashFetchErr()

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

// T5 — under the bounded budget a silent phone yields the typed timeout
// with the delivered-but-unanswered wording; the waiter is deregistered
// (the late completion is a dropped no-op) and a fresh attempt works.
func TestPeerRecoveryTimeoutDeregistersAndDropsLateEvent(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		a, fc := resyncAdapter(t)
		fc.FetchAppStateErr = lthashFetchErr()

		errCh := make(chan error, 1)
		go func() { errCh <- a.ResyncAppState(context.Background(), "regular_high", true) }()
		waitPeerRequest(t, fc, 1)
		synctest.Wait() // worker parked on the completion/deadline select

		err := <-errCh
		if !errors.Is(err, domain.ErrPeerRecoveryTimeout) {
			t.Fatalf("err = %v, want ErrPeerRecoveryTimeout", err)
		}
		if !strings.Contains(err.Error(), "delivered") {
			t.Fatalf("timeout error must distinguish delivered-but-silent, got: %v", err)
		}

		// Late completion after deregistration: dropped no-op — the map
		// entry is gone, so this must neither panic nor satisfy anything.
		a.handleWAEvent(completion(appstate.WAPatchRegularHigh, true))

		// A fresh attempt is possible immediately (single-flight cleared).
		go func() { errCh <- a.ResyncAppState(context.Background(), "regular_high", true) }()
		waitPeerRequest(t, fc, 1)
		a.handleWAEvent(completion(appstate.WAPatchRegularHigh, true))
		if err := <-errCh; err != nil {
			t.Fatalf("fresh attempt after timeout did not complete: %v", err)
		}
		assertNoChatSends(t, fc)
	})
}

// T6 — caller cancellation mid-wait unwinds with the context error and
// leaves no in-flight entry behind.
func TestPeerRecoveryContextCancellation(t *testing.T) {
	a, fc := resyncAdapter(t)
	fc.FetchAppStateErr = lthashFetchErr()

	ctx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error, 1)
	go func() { errCh <- a.ResyncAppState(ctx, "regular_high", true) }()
	waitPeerRequest(t, fc, 1)
	cancel()

	err := <-errCh
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
	// Deregistered: a fresh attempt refuses nothing and completes.
	go func() { errCh <- a.ResyncAppState(context.Background(), "regular_high", true) }()
	waitPeerRequest(t, fc, 1)
	a.handleWAEvent(completion(appstate.WAPatchRegularHigh, true))
	if err := <-errCh; err != nil {
		t.Fatalf("fresh attempt after cancellation did not complete: %v", err)
	}
}

// T7 — per-collection in-flight exclusion (acquired before the fetch,
// full or incremental): a second same-collection call refuses BEFORE any
// fetch and makes ZERO extra fetches/peer messages, while a different
// collection proceeds independently. Silence holds throughout (D8).
func TestPeerRecoverySingleFlightAndIndependence(t *testing.T) {
	a, fc := resyncAdapter(t)
	fc.FetchAppStateErr = lthashFetchErr()

	errCh := make(chan error, 2)
	go func() { errCh <- a.ResyncAppState(context.Background(), "regular_high", true) }()
	waitPeerRequest(t, fc, 1)

	fc.mu.Lock()
	fetchesAfterFirst := len(fc.FetchAppStateCalls)
	fc.mu.Unlock()

	// Second same-collection call: refused at the exclusion, zero extra
	// fetches of any kind.
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

// T7b — the exclusion also covers incremental syncs: while a full repair
// with its fallback is in flight for a collection, an incremental
// same-collection call refuses instead of interleaving with the
// mid-repair store.
func TestResyncIncrementalRefusedWhileRepairInFlight(t *testing.T) {
	a, fc := resyncAdapter(t)
	fc.FetchAppStateErr = lthashFetchErr()

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
