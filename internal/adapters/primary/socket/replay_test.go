package socket

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"regexp"
	"testing"
)

// fakeReplayer is an in-memory EventReplayer over a fixed row set,
// honouring the ascending-order + limit contract so the batching loop is
// exercised for real rather than assumed.
type fakeReplayer struct {
	rows      []ReplayRecord
	oldest    int64
	oldestErr error
	replayErr error
	calls     int
}

func (f *fakeReplayer) OldestSeq(context.Context) (int64, error) {
	if f.oldestErr != nil {
		return 0, f.oldestErr
	}
	return f.oldest, nil
}

func (f *fakeReplayer) Replay(_ context.Context, sinceSeq int64, limit int) ([]ReplayRecord, error) {
	f.calls++
	if f.replayErr != nil {
		return nil, f.replayErr
	}
	out := make([]ReplayRecord, 0, limit)
	for _, r := range f.rows {
		if r.Seq <= sinceSeq {
			continue
		}
		if len(out) == limit {
			break
		}
		out = append(out, r)
	}
	return out, nil
}

// drainFrames reads every queued frame off the connection mailbox without
// blocking, so a test can assert on the exact delivered sequence.
func drainFrames(t *testing.T, conn *Connection) []map[string]any {
	t.Helper()
	var got []map[string]any
	for {
		select {
		case raw := <-conn.out:
			var m map[string]any
			if err := json.Unmarshal(raw, &m); err != nil {
				t.Fatalf("unmarshal frame: %v", err)
			}
			got = append(got, m)
		default:
			return got
		}
	}
}

// seqsOf extracts the seq of every `event` notification, ignoring error
// frames (stream.drop), so ordering assertions read cleanly.
func seqsOf(frames []map[string]any) []int64 {
	var out []int64
	for _, f := range frames {
		if f["method"] != "event" {
			continue
		}
		params, _ := f["params"].(map[string]any)
		if params == nil {
			continue
		}
		if v, ok := params["seq"].(float64); ok {
			out = append(out, int64(v))
		}
	}
	return out
}

// newReplayHarness wires a server over the given ring plus a connection whose
// mailbox is deep enough that no test blocks on backpressure.
func newReplayHarness(t *testing.T, f *fakeReplayer, outBuf int) (*Server, *Connection) {
	t.Helper()
	s := NewServer(nil, slog.New(slog.DiscardHandler), WithEventReplay(f))
	conn := testConn(t)
	conn.out = make(chan []byte, outBuf)
	return s, conn
}

func rec(seq int64, kind string) ReplayRecord {
	return ReplayRecord{Seq: seq, Kind: kind, Data: []byte(`{"id":"m` + string(rune('0'+seq)) + `"}`)}
}

// TestReplaySubscriptionDeliversBufferedEvents pins the FR-061 MUST that
// the pre-#304 socket path did not satisfy: subscribe({since: N}) hands
// back the buffered events with seq > N, rather than only suppressing the
// ones at or below the cursor.
func TestReplaySubscriptionDeliversBufferedEvents(t *testing.T) {
	f := &fakeReplayer{oldest: 1, rows: []ReplayRecord{
		rec(1, "message"), rec(2, "message"), rec(3, "message"), rec(4, "message"),
	}}
	s, conn := newReplayHarness(t, f, 16)
	sub := &Subscription{id: "s1", since: 2, lastSeq: 2}

	s.replaySubscription(context.Background(), conn, sub)

	got := seqsOf(drainFrames(t, conn))
	if len(got) != 2 || got[0] != 3 || got[1] != 4 {
		t.Fatalf("replayed seqs = %v, want [3 4]", got)
	}
	if sub.lastSeq != 4 {
		t.Fatalf("lastSeq = %d, want 4", sub.lastSeq)
	}
}

// TestReplayThenLiveKeepsSeqOrdering is the ordering guarantee that lets
// replaySubscription run before registration: frames queued by the replay
// must reach the mailbox ahead of frames queued by a later fan-out, with
// no seq going backwards.
func TestReplayThenLiveKeepsSeqOrdering(t *testing.T) {
	f := &fakeReplayer{oldest: 1, rows: []ReplayRecord{rec(3, "message"), rec(4, "message")}}
	s, conn := newReplayHarness(t, f, 16)
	sub := &Subscription{id: "s1", since: 2, lastSeq: 2}

	s.replaySubscription(context.Background(), conn, sub)
	// Registration happens only after the replay, exactly as handleSubscribe
	// orders it; the live event that follows must land behind the replay.
	conn.subscriptions[sub.id] = sub
	s.conns[conn.id] = conn
	s.fanOutEvent(Event{Type: "message", Seq: 5})

	got := seqsOf(drainFrames(t, conn))
	if len(got) != 3 {
		t.Fatalf("got %d event frames (%v), want 3", len(got), got)
	}
	for i := 1; i < len(got); i++ {
		if got[i] <= got[i-1] {
			t.Fatalf("seq went backwards: %v", got)
		}
	}
	if got[len(got)-1] != 5 {
		t.Fatalf("live event should be last, got %v", got)
	}
}

// TestReplayHonoursFilterDSL pins that replayed rows go through the same
// matchesSub the live path uses. A replay that ignored the filter would
// deliver events the client explicitly excluded — the failure direction
// that actually leaks content.
func TestReplayHonoursFilterDSL(t *testing.T) {
	rows := []ReplayRecord{
		{Seq: 3, Kind: "message", Data: []byte(`{}`), Chat: "a@s.whatsapp.net", Sender: "a@s.whatsapp.net", Body: "hello"},
		{Seq: 4, Kind: "message", Data: []byte(`{}`), Chat: "b@s.whatsapp.net", Sender: "b@s.whatsapp.net", Body: "hello"},
		{Seq: 5, Kind: "receipt", Data: []byte(`{}`), Chat: "a@s.whatsapp.net"},
		{Seq: 6, Kind: "message", Data: []byte(`{}`), Chat: "a@s.whatsapp.net", Sender: "a@s.whatsapp.net", Body: "goodbye"},
	}
	tests := []struct {
		name string
		sub  *Subscription
		want []int64
	}{
		{
			name: "chats filter",
			sub:  &Subscription{id: "s", since: 2, lastSeq: 2, chats: []string{"a@s.whatsapp.net"}},
			want: []int64{3, 5, 6},
		},
		{
			name: "events filter",
			sub:  &Subscription{id: "s", since: 2, lastSeq: 2, events: map[string]struct{}{"receipt": {}}},
			want: []int64{5},
		},
		{
			name: "notSenders filter",
			sub:  &Subscription{id: "s", since: 2, lastSeq: 2, notSenders: []string{"a@s.whatsapp.net"}},
			want: []int64{4, 5},
		},
		{
			name: "bodyRe filter",
			sub:  &Subscription{id: "s", since: 2, lastSeq: 2, bodyReCompiled: regexp.MustCompile(`^hello$`)},
			want: []int64{3, 4},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			s, conn := newReplayHarness(t, &fakeReplayer{oldest: 1, rows: rows}, 16)

			s.replaySubscription(context.Background(), conn, tc.sub)

			got := seqsOf(drainFrames(t, conn))
			if len(got) != len(tc.want) {
				t.Fatalf("delivered %v, want %v", got, tc.want)
			}
			for i := range got {
				if got[i] != tc.want[i] {
					t.Fatalf("delivered %v, want %v", got, tc.want)
				}
			}
			// Whatever the filter excluded, the cursor still advances to
			// the newest row seen — otherwise the next live event would
			// look like a gap and emit a stream.drop for events the
			// client deliberately filtered out.
			if tc.sub.lastSeq != 6 {
				t.Fatalf("lastSeq = %d, want 6 (advances past filtered rows)", tc.sub.lastSeq)
			}
		})
	}
}

// TestReplayEmitsDropWhenCursorEvicted pins FR-063 at resume time: a
// cursor that has fallen off the back of the ring is named before the
// rows that survive start arriving.
func TestReplayEmitsDropWhenCursorEvicted(t *testing.T) {
	f := &fakeReplayer{oldest: 50, rows: []ReplayRecord{rec(50, "message")}}
	s, conn := newReplayHarness(t, f, 16)
	sub := &Subscription{id: "s1", since: 10, lastSeq: 10}

	s.replaySubscription(context.Background(), conn, sub)

	frames := drainFrames(t, conn)
	if len(frames) == 0 {
		t.Fatal("expected at least the drop frame")
	}
	errObj, ok := frames[0]["error"].(map[string]any)
	if !ok {
		t.Fatalf("first frame is not a stream.drop error frame: %v", frames[0])
	}
	data, _ := errObj["data"].(map[string]any)
	if data["oldest_dropped"] != float64(11) || data["newest_dropped"] != float64(49) {
		t.Fatalf("drop range = %v..%v, want 11..49", data["oldest_dropped"], data["newest_dropped"])
	}
}

// TestReplayNoDropWhenRingCoversCursor: the common resume case must stay
// silent. A spurious stream.drop would train consumers to ignore the one
// signal that means real data loss.
func TestReplayNoDropWhenRingCoversCursor(t *testing.T) {
	f := &fakeReplayer{oldest: 1, rows: []ReplayRecord{rec(3, "message")}}
	s, conn := newReplayHarness(t, f, 16)

	s.replaySubscription(context.Background(), conn, &Subscription{id: "s1", since: 2, lastSeq: 2})

	for _, f := range drainFrames(t, conn) {
		if f["error"] != nil {
			t.Fatalf("unexpected error frame on a covered cursor: %v", f)
		}
	}
}

// TestReplayBatchesUntilDrained pins the loop contract: a row count above
// replayBatch must take more than one Replay call and still deliver every
// row exactly once, in order.
func TestReplayBatchesUntilDrained(t *testing.T) {
	rows := make([]ReplayRecord, 0, replayBatch+7)
	for i := 1; i <= replayBatch+7; i++ {
		rows = append(rows, ReplayRecord{Seq: int64(i), Kind: "message", Data: []byte(`{}`)})
	}
	f := &fakeReplayer{oldest: 1, rows: rows}
	s, conn := newReplayHarness(t, f, len(rows)+4)

	s.replaySubscription(context.Background(), conn, &Subscription{id: "s1", since: 0, lastSeq: 0})

	got := seqsOf(drainFrames(t, conn))
	if len(got) != len(rows) {
		t.Fatalf("delivered %d rows, want %d", len(got), len(rows))
	}
	for i, seq := range got {
		if seq != int64(i+1) {
			t.Fatalf("row %d has seq %d, want %d", i, seq, i+1)
		}
	}
	if f.calls < 2 {
		t.Fatalf("Replay called %d time(s); a >batch row set must page", f.calls)
	}
}

// TestReplayErrorLeavesSubscriptionLive pins rule 27's reversibility
// posture: a ring read failure must not fail the subscribe. The client
// ends up attached and live — the pre-replay behaviour — rather than
// with no subscription at all.
func TestReplayErrorLeavesSubscriptionLive(t *testing.T) {
	f := &fakeReplayer{oldest: 1, replayErr: errors.New("events.db is toast")}
	s, conn := newReplayHarness(t, f, 16)
	sub := &Subscription{id: "s1", since: 2, lastSeq: 2}

	s.replaySubscription(context.Background(), conn, sub)

	if got := seqsOf(drainFrames(t, conn)); len(got) != 0 {
		t.Fatalf("failed replay delivered %v, want nothing", got)
	}
	// Registration is the caller's job and happens regardless; the
	// subscription must be usable live.
	conn.subscriptions[sub.id] = sub
	s.conns[conn.id] = conn
	s.fanOutEvent(Event{Type: "message", Seq: 9})
	if got := seqsOf(drainFrames(t, conn)); len(got) != 1 || got[0] != 9 {
		t.Fatalf("live delivery after failed replay = %v, want [9]", got)
	}
}

// TestReplaySkippedWithoutReplayer pins that the option is what turns the
// behaviour on: with no ring wired the daemon keeps the documented
// live-only semantics instead of erroring.
func TestReplaySkippedWithoutReplayer(t *testing.T) {
	s := NewServer(nil, slog.New(slog.DiscardHandler))
	if s.replay != nil {
		t.Fatal("replay should be nil without WithEventReplay")
	}
	conn := testConn(t)
	conn.out = make(chan []byte, 8)

	if _, err := s.handleSubscribe(context.Background(), conn, json.RawMessage(`{"events":[],"since":2}`)); err != nil {
		t.Fatalf("subscribe without a ring must still succeed: %v", err)
	}
	if got := drainFrames(t, conn); len(got) != 0 {
		t.Fatalf("live-only subscribe emitted %v, want nothing", got)
	}
}

// TestSubscribeReplaysFromCursor is the FR-061 assertion at the method
// boundary rather than the helper: subscribe({since: N}) must hand back
// the buffered events with seq > N. Every other test here calls
// replaySubscription directly, so without this one a regression that drops
// the call site from handleSubscribe would pass the whole suite.
func TestSubscribeReplaysFromCursor(t *testing.T) {
	f := &fakeReplayer{oldest: 1, rows: []ReplayRecord{
		rec(1, "message"), rec(2, "message"), rec(3, "message"),
	}}
	s, conn := newReplayHarness(t, f, 16)

	raw, err := s.handleSubscribe(context.Background(), conn, json.RawMessage(`{"events":[],"since":2}`))
	if err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	var res subscribeResult
	if err := json.Unmarshal(raw, &res); err != nil {
		t.Fatalf("unmarshal result: %v", err)
	}
	if res.Since != 2 {
		t.Errorf("echoed since = %d, want 2", res.Since)
	}

	got := seqsOf(drainFrames(t, conn))
	if len(got) != 1 || got[0] != 3 {
		t.Fatalf("subscribe replayed %v, want [3]", got)
	}
	if _, ok := conn.subscriptions[res.SubscriptionID]; !ok {
		t.Fatal("subscription must be registered for live fan-out after the replay")
	}
}

// TestSubscribeWithoutCursorDoesNotReplay pins the opt-in: a client that
// subscribes with no cursor wants the live stream, not a dump of the whole
// ring. Replaying there would flood a fresh consumer with up to 10 000
// events it never asked for.
func TestSubscribeWithoutCursorDoesNotReplay(t *testing.T) {
	f := &fakeReplayer{oldest: 1, rows: []ReplayRecord{rec(1, "message"), rec(2, "message")}}
	s, conn := newReplayHarness(t, f, 16)

	if _, err := s.handleSubscribe(context.Background(), conn, json.RawMessage(`{"events":[]}`)); err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	if f.calls != 0 {
		t.Fatalf("Replay called %d time(s) for a cursorless subscribe, want 0", f.calls)
	}
	if got := drainFrames(t, conn); len(got) != 0 {
		t.Fatalf("cursorless subscribe emitted %v, want nothing", got)
	}
}
