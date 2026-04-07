package porttest

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/yolo-labz/wa/internal/domain"
)

func testEventStream(t *testing.T, factory Factory) {
	t.Helper()

	mkEvent := func(id string) domain.Event {
		return domain.ConnectionEvent{ID: domain.EventID(id), TS: time.Now(), State: domain.ConnConnected}
	}

	t.Run("ES1_enqueued_delivered", func(t *testing.T) {
		a := factory(t)
		a.EnqueueEvent(mkEvent("e1"))
		ev, err := a.Next(context.Background())
		if err != nil {
			reportf(t, "EventStream", "Next", "ES1", "nil error", err.Error())
		}
		if ev == nil || ev.EventID() != "e1" {
			reportf(t, "EventStream", "Next", "ES1", "event e1", eventIDOr(ev))
		}
	})

	t.Run("ES2_deadline", func(t *testing.T) {
		a := factory(t)
		ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
		defer cancel()
		start := time.Now()
		_, err := a.Next(ctx)
		if !errors.Is(err, context.DeadlineExceeded) {
			reportf(t, "EventStream", "Next", "ES2", "context.DeadlineExceeded", errString(err))
		}
		if time.Since(start) > 500*time.Millisecond {
			reportf(t, "EventStream", "Next", "ES2", "return within ~50ms", time.Since(start).String())
		}
	})

	t.Run("ES3_order", func(t *testing.T) {
		a := factory(t)
		for _, id := range []string{"e1", "e2", "e3"} {
			a.EnqueueEvent(mkEvent(id))
		}
		for _, want := range []string{"e1", "e2", "e3"} {
			ev, err := a.Next(context.Background())
			if err != nil {
				reportf(t, "EventStream", "Next", "ES3", "nil error", err.Error())
				return
			}
			if string(ev.EventID()) != want {
				reportf(t, "EventStream", "Next", "ES3", want, string(ev.EventID()))
			}
		}
	})

	t.Run("ES4_no_ack_loss", func(t *testing.T) {
		a := factory(t)
		a.EnqueueEvent(mkEvent("a"))
		a.EnqueueEvent(mkEvent("b"))
		ev1, _ := a.Next(context.Background())
		// deliberately no Ack
		ev2, err := a.Next(context.Background())
		if err != nil {
			reportf(t, "EventStream", "Next", "ES4", "nil error", err.Error())
		}
		if ev1 == nil || ev2 == nil || ev1.EventID() == ev2.EventID() {
			reportf(t, "EventStream", "Next", "ES4", "two distinct events", "duplicate or missing")
		}
	})

	t.Run("ES5_ack_unknown", func(t *testing.T) {
		a := factory(t)
		defer func() {
			if r := recover(); r != nil {
				reportf(t, "EventStream", "Ack", "ES5", "typed error, never panic", "panic")
			}
		}()
		err := a.Ack(domain.EventID("does-not-exist"))
		if err == nil {
			reportf(t, "EventStream", "Ack", "ES5", "typed not-found error", "nil")
		}
	})

	t.Run("ES6_1000_events", func(t *testing.T) {
		a := factory(t)
		const n = 1000
		for i := 0; i < n; i++ {
			a.EnqueueEvent(mkEvent("x"))
		}
		seen := 0
		for i := 0; i < n; i++ {
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			_, err := a.Next(ctx)
			cancel()
			if err != nil {
				reportf(t, "EventStream", "Next", "ES6", "no drops", err.Error())
				return
			}
			seen++
		}
		if seen != n {
			reportf(t, "EventStream", "Next", "ES6", "1000 events", "fewer")
		}
	})
}

func eventIDOr(e domain.Event) string {
	if e == nil {
		return "nil"
	}
	return string(e.EventID())
}
