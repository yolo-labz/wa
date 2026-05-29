package whatsmeow

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/yolo-labz/wa/v2/internal/domain"
)

// countPending reports how many entries the historyReqs routing table
// currently holds for adapter a.
func countPending(a *Adapter) int {
	n := 0
	a.historyReqs.Range(func(_, _ any) bool {
		n++
		return true
	})
	return n
}

// waitForPending blocks until a holds at least `want` pending history
// reqs or the deadline elapses. Returns true on success.
func waitForPending(a *Adapter, want int, d time.Duration) bool {
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		if countPending(a) >= want {
			return true
		}
		<-time.After(time.Millisecond)
	}
	return false
}

// Disconnected: ForceHistorySync returns ErrDisconnected and sends
// nothing — an honest typed failure, never a silent no-op.
func TestForceHistorySync_Disconnected(t *testing.T) {
	fc := newFakeClient() // ConnectedFlag/LoggedInFlag default false
	a := newTestAdapter(t, fc)
	t.Cleanup(func() { _ = a.Close() })

	chat := domain.MustJID("15551234567@s.whatsapp.net")
	r, err := a.ForceHistorySync(context.Background(), chat, 10)
	if !errors.Is(err, domain.ErrDisconnected) {
		t.Fatalf("want ErrDisconnected; got %v", err)
	}
	if r.Connected || r.Requested {
		t.Errorf("want no send when disconnected; got %+v", r)
	}
	if n := countPending(a); n != 0 {
		t.Errorf("never-leak: want 0 pending; got %d", n)
	}
	if len(fc.SentMessages) != 0 {
		t.Errorf("disconnected force must not SendMessage; got %d", len(fc.SentMessages))
	}
}

// Chat-scoped happy path: a matching ON_DEMAND response (simulated via
// resolveHistoryReq) unblocks the force and is reported as delivered.
func TestForceHistorySync_ChatScoped_Delivered(t *testing.T) {
	fc := newFakeClient()
	fc.ConnectedFlag = true
	fc.LoggedInFlag = true
	a := newTestAdapter(t, fc)
	t.Cleanup(func() { _ = a.Close() })
	a.forceSyncTimeout = 2 * time.Second

	chat := domain.MustJID("15551234567@s.whatsapp.net")
	resCh := make(chan ForceSyncResult, 1)
	errCh := make(chan error, 1)
	go func() {
		r, err := a.ForceHistorySync(context.Background(), chat, 10)
		resCh <- r
		errCh <- err
	}()

	if !waitForPending(a, 1, time.Second) {
		t.Fatal("pending history req never registered")
	}
	if !a.resolveHistoryReq([]domain.Message{domain.TextMessage{Recipient: chat, Body: "new"}}) {
		t.Fatal("resolveHistoryReq did not deliver to the pending force")
	}

	r := <-resCh
	if err := <-errCh; err != nil {
		t.Fatalf("ForceHistorySync: %v", err)
	}
	if !r.Delivered || r.Received != 1 {
		t.Errorf("want Delivered=true Received=1; got %+v", r)
	}
	if !r.Connected || !r.Requested {
		t.Errorf("want Connected+Requested true; got %+v", r)
	}
	if r.TimedOut {
		t.Errorf("want TimedOut=false on delivery; got %+v", r)
	}
	if n := countPending(a); n != 0 {
		t.Errorf("never-leak: want 0 pending after return; got %d", n)
	}
}

// Chat-scoped timeout: no matching response within the (shrunk) deadline
// yields TimedOut=true and still cleans up the routing entry.
func TestForceHistorySync_ChatScoped_Timeout(t *testing.T) {
	fc := newFakeClient()
	fc.ConnectedFlag = true
	fc.LoggedInFlag = true
	a := newTestAdapter(t, fc)
	t.Cleanup(func() { _ = a.Close() })
	a.forceSyncTimeout = 30 * time.Millisecond

	chat := domain.MustJID("15551234567@s.whatsapp.net")
	r, err := a.ForceHistorySync(context.Background(), chat, 10)
	if err != nil {
		t.Fatalf("ForceHistorySync: %v", err)
	}
	if !r.TimedOut || r.Delivered {
		t.Errorf("want TimedOut=true Delivered=false; got %+v", r)
	}
	if !r.Requested || !r.Connected {
		t.Errorf("want Requested+Connected true even on timeout; got %+v", r)
	}
	if n := countPending(a); n != 0 {
		t.Errorf("never-leak: want 0 pending after timeout; got %d", n)
	}
}

// Global path: no chat JID means no pending entry to await — the request
// is built + fired and persistence is left to the background worker
// (Async=true). Confirms the request was built and forwarded the count.
func TestForceHistorySync_Global_Async(t *testing.T) {
	fc := newFakeClient()
	fc.ConnectedFlag = true
	fc.LoggedInFlag = true
	a := newTestAdapter(t, fc)
	t.Cleanup(func() { _ = a.Close() })

	r, err := a.ForceHistorySync(context.Background(), domain.JID{}, 25)
	if err != nil {
		t.Fatalf("ForceHistorySync(global): %v", err)
	}
	if !r.Async || !r.Connected || !r.Requested {
		t.Errorf("want Async+Connected+Requested true; got %+v", r)
	}
	if r.Chat != "" {
		t.Errorf("want empty chat for global pull; got %q", r.Chat)
	}
	if len(fc.BuildHSReqs) != 1 {
		t.Fatalf("want 1 BuildHistorySyncRequest; got %d", len(fc.BuildHSReqs))
	}
	if got := fc.BuildHSReqs[0].Count; got != 25 {
		t.Errorf("want count=25 forwarded to BuildHistorySyncRequest; got %d", got)
	}
	if n := countPending(a); n != 0 {
		t.Errorf("global path must not register a pending entry; got %d", n)
	}
}

// count is defaulted when ≤0 and capped at historyRoundTripCap.
func TestForceHistorySync_CountDefaultedAndCapped(t *testing.T) {
	fc := newFakeClient()
	fc.ConnectedFlag = true
	fc.LoggedInFlag = true
	a := newTestAdapter(t, fc)
	t.Cleanup(func() { _ = a.Close() })

	r, err := a.ForceHistorySync(context.Background(), domain.JID{}, 0)
	if err != nil {
		t.Fatalf("ForceHistorySync: %v", err)
	}
	if r.Count != historyRoundTripCap {
		t.Errorf("count≤0 should default to %d; got %d", historyRoundTripCap, r.Count)
	}

	r2, err := a.ForceHistorySync(context.Background(), domain.JID{}, historyRoundTripCap+100)
	if err != nil {
		t.Fatalf("ForceHistorySync: %v", err)
	}
	if r2.Count != historyRoundTripCap {
		t.Errorf("count over cap should clamp to %d; got %d", historyRoundTripCap, r2.Count)
	}
}

// SyncStatus reflects queue capacity, idle depth, and in-flight reqs.
func TestSyncStatus_Snapshot(t *testing.T) {
	fc := newFakeClient()
	a := newTestAdapter(t, fc)
	t.Cleanup(func() { _ = a.Close() })

	s := a.SyncStatus()
	if s.QueueCap != historySyncChCap {
		t.Errorf("want QueueCap=%d; got %d", historySyncChCap, s.QueueCap)
	}
	if s.QueueDepth != 0 || s.InFlightForceReqs != 0 || s.Syncing {
		t.Errorf("want idle snapshot (depth/inflight/syncing all zero); got %+v", s)
	}

	// A registered pending surfaces as one in-flight force req.
	const k = historyReqSeq(1 << 40) // arbitrary, unique within this fresh adapter
	a.historyReqs.Store(k, &pendingHistoryReq{chatJID: "x", msgs: make(chan []domain.Message, 1)})
	defer a.historyReqs.Delete(k)
	if s2 := a.SyncStatus(); s2.InFlightForceReqs != 1 {
		t.Errorf("want InFlightForceReqs=1 with one pending; got %d", s2.InFlightForceReqs)
	}
}
