package whatsmeow

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/yolo-labz/wa/v2/internal/domain"
)

// syncHistory is a minimal historyContainer for the on-demand sync tests.
// It embeds the interface (left nil) so only the anchor methods the sync
// path actually calls — NewestRef + RecentChats — need real bodies; any
// other method would panic, which is the point: those paths must not be
// reached by ForceHistorySync. PR #222.
type syncHistory struct {
	historyContainer
	newest map[string]domain.MessageRef
	chats  []domain.JID
}

func (h *syncHistory) NewestRef(_ context.Context, chat domain.JID) (domain.MessageRef, bool, error) {
	r, ok := h.newest[chat.String()]
	return r, ok, nil
}

func (h *syncHistory) RecentChats(_ context.Context, _ int) ([]domain.JID, error) {
	return h.chats, nil
}

// Close is the one embedded method actually invoked (via Adapter.Close in
// test cleanup); override it so the nil embedded interface is never reached.
func (h *syncHistory) Close() error { return nil }

// anchorRef builds a stored-message reference for chat, for use as the test
// history's newest anchor.
func anchorRef(chat domain.JID) domain.MessageRef {
	return domain.MessageRef{Chat: chat, ID: "anchor-id", FromMe: false, Timestamp: time.Unix(1_700_000_000, 0)}
}

// withNewest wires a as having one stored chat whose newest message is the
// returned anchor — enough for ForceHistorySync to resolve an anchor.
func withNewest(a *Adapter, chat domain.JID) {
	a.history = &syncHistory{
		newest: map[string]domain.MessageRef{chat.String(): anchorRef(chat)},
		chats:  []domain.JID{chat},
	}
}

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

// Chat-scoped happy path: anchored at the chat's newest stored message, a
// matching ON_DEMAND response (simulated via resolveHistoryReq) unblocks the
// force and is reported as delivered.
func TestForceHistorySync_ChatScoped_Delivered(t *testing.T) {
	fc := newFakeClient()
	fc.ConnectedFlag = true
	fc.LoggedInFlag = true
	a := newTestAdapter(t, fc)
	t.Cleanup(func() { _ = a.Close() })
	a.forceSyncTimeout = 2 * time.Second

	chat := domain.MustJID("15551234567@s.whatsapp.net")
	withNewest(a, chat)
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
	// The request must carry a non-nil anchor (the wa-burocracy crash was a
	// nil anchor). PR #222.
	if len(fc.BuildHSReqs) != 1 || fc.BuildHSReqs[0].LastKnown == nil {
		t.Errorf("want 1 anchored BuildHistorySyncRequest; got %+v", fc.BuildHSReqs)
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
	withNewest(a, chat)
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

// Chat-scoped with no stored message to anchor against: an honest no-op —
// Requested stays false, nothing is sent, nothing panics. PR #222.
func TestForceHistorySync_ChatScoped_NoAnchor(t *testing.T) {
	fc := newFakeClient()
	fc.ConnectedFlag = true
	fc.LoggedInFlag = true
	a := newTestAdapter(t, fc)
	t.Cleanup(func() { _ = a.Close() })
	a.history = &syncHistory{} // NewestRef → ok=false for every chat

	chat := domain.MustJID("15551234567@s.whatsapp.net")
	r, err := a.ForceHistorySync(context.Background(), chat, 10)
	if err != nil {
		t.Fatalf("ForceHistorySync: %v", err)
	}
	if r.Requested {
		t.Errorf("want Requested=false with no anchor; got %+v", r)
	}
	if len(fc.BuildHSReqs) != 0 {
		t.Errorf("no anchor must not build a request; got %d", len(fc.BuildHSReqs))
	}
	if n := countPending(a); n != 0 {
		t.Errorf("never-leak: want 0 pending; got %d", n)
	}
}

// Global path: sweep the recently-active chats, anchor each at its newest
// stored message, fire one pull per chat. Async; persistence is the worker's
// job. Confirms an anchored request was built and forwarded the count.
func TestForceHistorySync_Global_Async(t *testing.T) {
	fc := newFakeClient()
	fc.ConnectedFlag = true
	fc.LoggedInFlag = true
	a := newTestAdapter(t, fc)
	t.Cleanup(func() { _ = a.Close() })
	chat := domain.MustJID("15551234567@s.whatsapp.net")
	withNewest(a, chat)

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
		t.Fatalf("want 1 BuildHistorySyncRequest (one recent chat); got %d", len(fc.BuildHSReqs))
	}
	if fc.BuildHSReqs[0].LastKnown == nil {
		t.Error("want a non-nil anchor on the global pull")
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
	withNewest(a, domain.MustJID("15551234567@s.whatsapp.net"))

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

// sendHistoryRequest with a nil anchor is a no-op, never a panic. This is the
// exact regression that crashed wa-burocracy: whatsmeow's
// BuildHistorySyncRequest dereferences the anchor, and the fake replicates
// that contract — so reaching it with nil would panic this test. PR #222.
func TestSendHistoryRequest_NilAnchorIsNoOp(t *testing.T) {
	fc := newFakeClient()
	fc.ConnectedFlag = true
	fc.LoggedInFlag = true
	a := newTestAdapter(t, fc)
	t.Cleanup(func() { _ = a.Close() })

	a.sendHistoryRequest(nil, 10)
	if len(fc.BuildHSReqs) != 0 {
		t.Errorf("nil anchor must not build a request; got %d", len(fc.BuildHSReqs))
	}
	if len(fc.SentMessages) != 0 {
		t.Errorf("nil anchor must not send; got %d", len(fc.SentMessages))
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
