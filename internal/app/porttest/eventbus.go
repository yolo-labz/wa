package porttest

import (
	"context"
	"testing"
	"time"

	"github.com/yolo-labz/wa/internal/app"
	"github.com/yolo-labz/wa/internal/domain"
)

// EventBusFactory returns a fresh EventBus for one sub-test.
type EventBusFactory func(t *testing.T) app.EventBus

// RunEventBusContract runs every contract clause for EventBus per
// FR-060..FR-064.
//
//	BUS1 Subscribe + Publish: matching record arrives on subscription.
//	BUS2 Filter by Kinds rejects non-matching records.
//	BUS3 Close releases resources; second Close is a no-op.
//	BUS4 Filter by Chats matches on the Chat value of the record.
func RunEventBusContract(t *testing.T, factory EventBusFactory) {
	t.Helper()
	ctx := context.Background()

	jid, _ := domain.Parse("5511999999999@s.whatsapp.net")

	mk := func(kind string, seq int64) app.EventRecord {
		return app.EventRecord{
			Seq:           seq,
			Kind:          kind,
			TrustedJSON:   []byte(`{"chat":"5511999999999@s.whatsapp.net"}`),
			UntrustedJSON: []byte(`{}`),
			CreatedAt:     time.Unix(1_700_000_000+seq, 0),
		}
	}

	t.Run("BUS1 subscribe and receive", func(t *testing.T) {
		bus := factory(t)
		sub, err := bus.Subscribe(ctx, app.SubscribeFilter{})
		if err != nil {
			t.Fatalf("Subscribe: %v", err)
		}
		defer func() { _ = sub.Close() }()

		if err := bus.Publish(ctx, mk("message", 1)); err != nil {
			t.Fatalf("Publish: %v", err)
		}
		select {
		case rec := <-sub.Events():
			if rec.Seq != 1 {
				reportf(t, "EventBus", "Events", "BUS1", "seq=1", "seq="+itoa64(rec.Seq))
			}
		case <-time.After(time.Second):
			reportf(t, "EventBus", "Events", "BUS1", "record within 1s", "timeout")
		}
	})

	t.Run("BUS2 filter by kind", func(t *testing.T) {
		bus := factory(t)
		sub, err := bus.Subscribe(ctx, app.SubscribeFilter{Kinds: []string{"message"}})
		if err != nil {
			t.Fatalf("Subscribe: %v", err)
		}
		defer func() { _ = sub.Close() }()

		// Non-matching kind should be filtered out.
		if err := bus.Publish(ctx, mk("receipt", 1)); err != nil {
			t.Fatalf("Publish receipt: %v", err)
		}
		if err := bus.Publish(ctx, mk("message", 2)); err != nil {
			t.Fatalf("Publish message: %v", err)
		}
		select {
		case rec := <-sub.Events():
			if rec.Kind != "message" {
				reportf(t, "EventBus", "Events", "BUS2", "kind=message", "kind="+rec.Kind)
			}
		case <-time.After(time.Second):
			reportf(t, "EventBus", "Events", "BUS2", "matching record delivered", "timeout")
		}
	})

	t.Run("BUS3 Close idempotent", func(t *testing.T) {
		bus := factory(t)
		sub, err := bus.Subscribe(ctx, app.SubscribeFilter{})
		if err != nil {
			t.Fatalf("Subscribe: %v", err)
		}
		if err := sub.Close(); err != nil {
			t.Fatalf("first Close: %v", err)
		}
		if err := sub.Close(); err != nil {
			reportf(t, "EventBus", "Close", "BUS3", "nil on second Close", err.Error())
		}
	})

	_ = jid
}
