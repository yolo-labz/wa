package app

import (
	"log/slog"
	"testing"
	"time"

	"github.com/yolo-labz/wa/v2/internal/domain"
)

// recvSeq reads one event off ch or fails the test. Returns the seq so
// the callers below read as a list of sequence numbers rather than a
// wall of select statements.
func recvSeq(t *testing.T, ch <-chan Event, what string) int64 {
	t.Helper()
	select {
	case evt := <-ch:
		return evt.Seq
	case <-time.After(2 * time.Second):
		t.Fatalf("timed out waiting for %s", what)
		return 0
	}
}

// The socket adapter's FR-063 gap detection and its `subscribe({since})`
// resume cursor are both written to skip when Seq is 0 — which is what
// every event carried, because nothing ever assigned one. Numbering has
// to happen at the single fan-out point: assign it per-consumer and two
// subscribers would disagree about which event seq 7 was.
func TestEventBridge_SeqIsMonotonicAndSharedAcrossConsumers(t *testing.T) {
	now := time.Now()
	fs := &fakeStream{}
	bridge := NewEventBridge(fs, slog.Default())
	sub, cancel := bridge.SubscribeStream(nil, 8)
	defer cancel()
	go bridge.Run()

	fs.enqueue(
		domain.MessageEvent{ID: "1", TS: now, From: testJID(t)},
		domain.ReceiptEvent{ID: "2", TS: now},
	)
	for i, want := range []int64{1, 2} {
		if got := recvSeq(t, bridge.Events(), "upstream event"); got != want {
			t.Fatalf("Events()[%d].Seq = %d, want %d", i, got, want)
		}
		if got := recvSeq(t, sub, "upstream event on subscriber"); got != want {
			t.Fatalf("subscriber[%d].Seq = %d, want %d — consumers disagree on which event this is", i, got, want)
		}
	}

	// Synthetic events (the spec-110g watchdog signals) share the
	// numbering: skip them and a subscriber sees seq jump, which the
	// socket server reports as a stream.drop that never happened.
	bridge.EmitSynthetic(domain.ConnectionEvent{ID: "3", TS: now, State: domain.ConnConnected})
	if got := recvSeq(t, bridge.Events(), "synthetic event"); got != 3 {
		t.Errorf("synthetic event Seq = %d, want 3", got)
	}
	if got := recvSeq(t, sub, "synthetic event on subscriber"); got != 3 {
		t.Errorf("synthetic event subscriber Seq = %d, want 3", got)
	}

	bridge.Close()
}

// A daemon restart must not reissue sequence numbers a subscriber already
// acked (FR-062), so the composition root seeds the counter from the
// durable ring's newest row. The seed rides in on DispatcherConfig because
// NewDispatcher starts the bridge goroutine — a setter on the returned
// Dispatcher would race the first inbound event.
func TestNewDispatcher_EventSeqSeedResumesNumbering(t *testing.T) {
	fs := &fakeStream{}
	d := NewDispatcher(DispatcherConfig{
		Events:       fs,
		Logger:       slog.New(slog.DiscardHandler),
		EventSeqSeed: 41,
	})
	defer func() {
		if err := d.Close(); err != nil {
			t.Errorf("Close: %v", err)
		}
	}()

	fs.enqueue(domain.ReceiptEvent{ID: "1", TS: time.Now()})
	if got := recvSeq(t, d.Events(), "first event after restart"); got != 42 {
		t.Errorf("first Seq after a seed of 41 = %d, want 42", got)
	}
}
