package app

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/yolo-labz/wa/internal/domain"
	"go.uber.org/goleak"
)

func TestMain(m *testing.M) {
	goleak.VerifyTestMain(m)
}

// fakeStream is a test EventStream that returns pre-loaded events.
type fakeStream struct {
	mu     sync.Mutex
	events []domain.Event
	errFn  func() error // optional: return error before events
}

func (f *fakeStream) Next(ctx context.Context) (domain.Event, error) {
	if f.errFn != nil {
		if err := f.errFn(); err != nil {
			return nil, err
		}
	}
	f.mu.Lock()
	if len(f.events) > 0 {
		evt := f.events[0]
		f.events = f.events[1:]
		f.mu.Unlock()
		return evt, nil
	}
	f.mu.Unlock()
	// No events queued; block until context is done.
	<-ctx.Done()
	return nil, ctx.Err()
}

func (f *fakeStream) Ack(_ domain.EventID) error { return nil }

func (f *fakeStream) enqueue(evts ...domain.Event) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.events = append(f.events, evts...)
}

// TestEventBridge_DeliveryOrder verifies 3 events are delivered in order.
func TestEventBridge_DeliveryOrder(t *testing.T) {
	now := time.Now()
	events := []domain.Event{
		domain.MessageEvent{ID: "1", TS: now, From: testJID(t)},
		domain.ReceiptEvent{ID: "2", TS: now},
		domain.ConnectionEvent{ID: "3", TS: now, State: domain.ConnConnected},
	}
	fs := &fakeStream{events: events}
	bridge := NewEventBridge(fs, slog.Default())
	go bridge.Run()

	var received []Event
	timeout := time.After(2 * time.Second)
	for i := range 3 {
		select {
		case evt := <-bridge.Events():
			received = append(received, evt)
		case <-timeout:
			t.Fatalf("timed out waiting for event %d", i+1)
		}
	}

	wantTypes := []string{"message", "receipt", "status"}
	for i, want := range wantTypes {
		if received[i].Type != want {
			t.Errorf("event[%d].Type = %q, want %q", i, received[i].Type, want)
		}
	}

	bridge.Close()
}

// TestEventBridge_WaiterFilter verifies that waiters only receive
// events matching their filter.
func TestEventBridge_WaiterFilter(t *testing.T) {
	now := time.Now()
	jid := testJID(t)
	fs := &fakeStream{}
	bridge := NewEventBridge(fs, slog.Default())
	go bridge.Run()

	// Register waiter for "receipt" events only.
	ch, cancel := bridge.RegisterWaiter([]string{"receipt"})
	defer cancel()

	// Enqueue a message event (should NOT match) then a receipt event.
	fs.enqueue(
		domain.MessageEvent{ID: "1", TS: now, From: jid},
		domain.ReceiptEvent{ID: "2", TS: now},
	)

	// Drain the main Events() channel to let the bridge process.
	go func() {
		for range bridge.Events() {
		}
	}()

	select {
	case evt := <-ch:
		if evt.Type != "receipt" {
			t.Errorf("waiter got Type=%q, want receipt", evt.Type)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for receipt event on waiter")
	}

	bridge.Close()
}

// TestEventBridge_ShutdownClosesChannel verifies that Close() causes
// Events() to be closed and no goroutines leak.
func TestEventBridge_ShutdownClosesChannel(t *testing.T) {
	fs := &fakeStream{}
	bridge := NewEventBridge(fs, slog.Default())
	go bridge.Run()

	bridge.Close()

	// Events() channel should be closed.
	_, ok := <-bridge.Events()
	if ok {
		t.Error("Events() channel should be closed after Close()")
	}
	// goleak.VerifyTestMain catches any leaked goroutines.
}

// T032: wait returns matching event.
func TestEventBridge_WaitReturnsMatchingEvent(t *testing.T) {
	now := time.Now()
	jid := testJID(t)
	fs := &fakeStream{}
	bridge := NewEventBridge(fs, slog.Default())
	go bridge.Run()

	ch, cancel := bridge.RegisterWaiter([]string{"message"})
	defer cancel()

	// Drain Events() so the bridge doesn't block.
	go func() {
		for range bridge.Events() {
		}
	}()

	// Enqueue a matching event.
	fs.enqueue(domain.MessageEvent{ID: "1", TS: now, From: jid})

	select {
	case evt := <-ch:
		if evt.Type != "message" {
			t.Errorf("waiter got Type=%q, want message", evt.Type)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for matching event")
	}

	bridge.Close()
}

// T033: wait times out when no event arrives.
func TestEventBridge_WaitTimeout(t *testing.T) {
	fs := &fakeStream{} // no events
	bridge := NewEventBridge(fs, slog.Default())
	go bridge.Run()

	ch, cancel := bridge.RegisterWaiter(nil)
	defer cancel()

	ctx, ctxCancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer ctxCancel()

	select {
	case <-ch:
		t.Fatal("unexpected event received")
	case <-ctx.Done():
		// Expected — timeout.
	}

	bridge.Close()
}

// T034: wait filters by event type — non-matching events are skipped.
func TestEventBridge_WaitFiltersByType(t *testing.T) {
	now := time.Now()
	jid := testJID(t)
	fs := &fakeStream{}
	bridge := NewEventBridge(fs, slog.Default())
	go bridge.Run()

	// Only want "receipt" events.
	ch, cancel := bridge.RegisterWaiter([]string{"receipt"})
	defer cancel()

	// Drain the Events() channel.
	go func() {
		for range bridge.Events() {
		}
	}()

	// Enqueue a non-matching event first, then a matching one.
	fs.enqueue(
		domain.MessageEvent{ID: "1", TS: now, From: jid},
		domain.ReceiptEvent{ID: "2", TS: now},
	)

	select {
	case evt := <-ch:
		if evt.Type != "receipt" {
			t.Errorf("waiter got Type=%q, want receipt", evt.Type)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for receipt")
	}

	bridge.Close()
}

// T035: bridge delivers to both Events() and wait waiter simultaneously.
func TestEventBridge_DeliversToBothEventsAndWaiter(t *testing.T) {
	now := time.Now()
	jid := testJID(t)
	fs := &fakeStream{}
	bridge := NewEventBridge(fs, slog.Default())
	go bridge.Run()

	// Register a waiter for all events.
	waiterCh, cancel := bridge.RegisterWaiter(nil)
	defer cancel()

	fs.enqueue(domain.MessageEvent{ID: "1", TS: now, From: jid})

	// Read from both channels.
	var gotEvents, gotWaiter bool
	timeout := time.After(2 * time.Second)
	for !gotEvents || !gotWaiter {
		select {
		case <-bridge.Events():
			gotEvents = true
		case <-waiterCh:
			gotWaiter = true
		case <-timeout:
			t.Fatalf("timed out: gotEvents=%v, gotWaiter=%v", gotEvents, gotWaiter)
		}
	}

	bridge.Close()
}

// TestEventBridge_ErrorRetry verifies that non-cancel errors cause a
// retry rather than shutdown.
func TestEventBridge_ErrorRetry(t *testing.T) {
	now := time.Now()
	jid := testJID(t)
	callCount := 0
	var mu sync.Mutex
	fs := &fakeStream{
		events: []domain.Event{
			domain.MessageEvent{ID: "1", TS: now, From: jid},
		},
		errFn: func() error {
			mu.Lock()
			defer mu.Unlock()
			callCount++
			if callCount <= 2 {
				return errors.New("transient error")
			}
			return nil // let events through on 3rd+ call
		},
	}

	bridge := NewEventBridge(fs, slog.Default())
	go bridge.Run()

	select {
	case evt := <-bridge.Events():
		if evt.Type != "message" {
			t.Errorf("got Type=%q, want message", evt.Type)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for event after retries")
	}

	bridge.Close()
}

// TestEventBridge_ConcurrentCancelDuringFanOut verifies no deadlock when
// a waiter cancel fires concurrently with event fan-out.  Regression test
// for C-001 (copy-under-lock pattern).
func TestEventBridge_ConcurrentCancelDuringFanOut(t *testing.T) {
	now := time.Now()
	jid := testJID(t)

	// Pre-load many events so the bridge is busy during cancellation.
	const eventCount = 50
	events := make([]domain.Event, eventCount)
	for i := range events {
		events[i] = domain.MessageEvent{ID: domain.EventID(string(rune('A' + i%26))), TS: now, From: jid}
	}
	fs := &fakeStream{events: events}

	bridge := NewEventBridge(fs, slog.Default())
	go bridge.Run()

	// Register many waiters.
	const waiterCount = 20
	cancels := make([]func(), waiterCount)
	for i := range waiterCount {
		_, cancels[i] = bridge.RegisterWaiter(nil)
		_ = i
	}

	// Fire all cancels concurrently while events are being delivered.
	var wg sync.WaitGroup
	wg.Add(waiterCount)
	for _, cancel := range cancels {
		go func() {
			defer wg.Done()
			cancel()
		}()
	}
	wg.Wait()

	// Drain the events channel to let the bridge finish.
	done := make(chan struct{})
	go func() {
		for range bridge.Events() {
		}
		close(done)
	}()

	bridge.Close()
	<-done
}
