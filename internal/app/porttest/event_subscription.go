package porttest

import (
	"context"
	"testing"
	"time"

	"github.com/yolo-labz/wa/v2/internal/app"
)

// EventSubscriptionFactory returns a fresh EventBus for one sub-test.
// The runner opens its own Subscribe handle per clause so that
// Close-idempotency and Ack semantics are exercised cleanly.
type EventSubscriptionFactory func(t *testing.T) app.EventBus

// RunEventSubscriptionContract exercises EventSubscription (FR-060..064).
//
//	SUB1 Events() returns a receive-only channel that delivers published
//	     records in Seq order.
//	SUB2 Ack(seq) returns nil for any seq already observed on Events().
//	SUB3 Close is idempotent: second call returns nil.
//	SUB4 Events() channel is closed after Close.
func RunEventSubscriptionContract(t *testing.T, factory EventSubscriptionFactory) {
	t.Helper()
	ctx := context.Background()

	mk := func(kind string, seq int64) app.EventRecord {
		return app.EventRecord{
			Seq:           seq,
			Kind:          kind,
			TrustedJSON:   []byte(`{}`),
			UntrustedJSON: []byte(`{}`),
			CreatedAt:     time.Unix(1_700_000_000+seq, 0),
		}
	}

	t.Run("SUB1_events_ordered", func(t *testing.T) {
		bus := factory(t)
		sub, err := bus.Subscribe(ctx, app.SubscribeFilter{})
		if err != nil {
			t.Fatalf("Subscribe: %v", err)
		}
		defer func() { _ = sub.Close() }()

		for i := int64(1); i <= 3; i++ {
			if err := bus.Publish(ctx, mk("message", i)); err != nil {
				t.Fatalf("Publish: %v", err)
			}
		}
		var seen []int64
		deadline := time.After(2 * time.Second)
		for len(seen) < 3 {
			select {
			case rec := <-sub.Events():
				seen = append(seen, rec.Seq)
			case <-deadline:
				reportf(t, "EventSubscription", "Events", "SUB1", "3 records within 2s", "timeout")
				return
			}
		}
		for i := 1; i < len(seen); i++ {
			if seen[i] <= seen[i-1] {
				reportf(t, "EventSubscription", "Events", "SUB1", "monotonically increasing seq", "out-of-order")
				return
			}
		}
	})

	t.Run("SUB2_ack_observed", func(t *testing.T) {
		bus := factory(t)
		sub, err := bus.Subscribe(ctx, app.SubscribeFilter{})
		if err != nil {
			t.Fatalf("Subscribe: %v", err)
		}
		defer func() { _ = sub.Close() }()

		if err := bus.Publish(ctx, mk("message", 7)); err != nil {
			t.Fatalf("Publish: %v", err)
		}
		select {
		case rec := <-sub.Events():
			if err := sub.Ack(rec.Seq); err != nil {
				reportf(t, "EventSubscription", "Ack", "SUB2", "nil error", err.Error())
			}
		case <-time.After(time.Second):
			reportf(t, "EventSubscription", "Events", "SUB2", "record before ack", "timeout")
		}
	})

	t.Run("SUB3_close_idempotent", func(t *testing.T) {
		bus := factory(t)
		sub, err := bus.Subscribe(ctx, app.SubscribeFilter{})
		if err != nil {
			t.Fatalf("Subscribe: %v", err)
		}
		if err := sub.Close(); err != nil {
			t.Fatalf("first Close: %v", err)
		}
		if err := sub.Close(); err != nil {
			reportf(t, "EventSubscription", "Close", "SUB3", "nil on second Close", err.Error())
		}
	})

	t.Run("SUB4_events_closed_after_close", func(t *testing.T) {
		bus := factory(t)
		sub, err := bus.Subscribe(ctx, app.SubscribeFilter{})
		if err != nil {
			t.Fatalf("Subscribe: %v", err)
		}
		if err := sub.Close(); err != nil {
			t.Fatalf("Close: %v", err)
		}
		select {
		case _, ok := <-sub.Events():
			if ok {
				reportf(t, "EventSubscription", "Events", "SUB4", "closed channel", "open channel delivered record")
			}
		case <-time.After(time.Second):
			reportf(t, "EventSubscription", "Events", "SUB4", "channel closed within 1s", "still open")
		}
	})
}
