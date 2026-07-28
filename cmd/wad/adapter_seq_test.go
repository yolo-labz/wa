package main

import (
	"context"
	"testing"
	"time"

	"github.com/yolo-labz/wa/v2/internal/app"
	"github.com/yolo-labz/wa/v2/internal/domain"
)

// The socket server reads socket.Event.Seq; the bridge stamps
// app.Event.Seq. This adapter is the only thing between them, and it
// rebuilt the socket event field by field — so Seq arrived as 0, and the
// server's two seq-aware paths are both written to skip on 0: no `seq` on
// any notification frame, no FR-063 stream.drop, and `subscribe({since})`
// re-delivering events the client already acked.
func TestDispatcherAdapter_ForwardsSeq(t *testing.T) {
	stream := newReplayStream()
	d := app.NewDispatcher(app.DispatcherConfig{Events: stream, Logger: quietLogger()})
	ctx, cancel := context.WithCancel(context.Background())
	da := newDispatcherAdapter(ctx, d, nil)
	t.Cleanup(func() {
		cancel()
		da.Close()
		_ = d.Close()
	})

	now := time.Now()
	stream.push(domain.ReceiptEvent{ID: "1", TS: now})
	stream.push(domain.ReceiptEvent{ID: "2", TS: now})

	for _, want := range []int64{1, 2} {
		select {
		case se := <-da.Events():
			if se.Seq != want {
				t.Fatalf("socket.Event.Seq = %d, want %d", se.Seq, want)
			}
		case <-time.After(2 * time.Second):
			t.Fatalf("timed out waiting for event with seq %d", want)
		}
	}
}
