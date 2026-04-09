package app

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/yolo-labz/wa/internal/domain"
)

// waiter represents a registered wait caller blocking for a matching event.
type waiter struct {
	filter map[string]struct{}
	ch     chan Event
}

// matches returns true if the event type is in the filter, or if the
// filter is empty (match all).
func (w *waiter) matches(eventType string) bool {
	if len(w.filter) == 0 {
		return true
	}
	_, ok := w.filter[eventType]
	return ok
}

// EventBridge reads from the pull-based EventStream port and fans out
// events to both the Events() channel and registered wait waiters.
type EventBridge struct {
	stream EventStream
	out    chan Event
	log    *slog.Logger

	mu      sync.Mutex
	waiters []*waiter

	ctx    context.Context
	cancel context.CancelFunc
	done   chan struct{}
}

// NewEventBridge creates an event bridge that reads from the given
// EventStream. Call Run() to start the bridge goroutine.
func NewEventBridge(stream EventStream, log *slog.Logger) *EventBridge {
	if log == nil {
		log = slog.Default()
	}
	ctx, cancel := context.WithCancel(context.Background())
	return &EventBridge{
		stream: stream,
		out:    make(chan Event, 64),
		log:    log,
		ctx:    ctx,
		cancel: cancel,
		done:   make(chan struct{}),
	}
}

// Run starts the bridge goroutine. It blocks until the bridge's context
// is cancelled (via Close). Typically called as `go b.Run()`.
func (b *EventBridge) Run() {
	defer close(b.done)
	defer close(b.out)

	for {
		evt, err := b.stream.Next(b.ctx)
		if err != nil {
			if b.ctx.Err() != nil {
				return // shutdown
			}
			b.log.Error("EventBridge: stream error, retrying", "error", err)
			// Backoff before retry per FR-035.
			select {
			case <-time.After(100 * time.Millisecond):
			case <-b.ctx.Done():
				return
			}
			continue
		}

		appEvt := translateDomainEvent(evt)

		// Push to the main Events() channel (non-blocking).
		select {
		case b.out <- appEvt:
		default:
			b.log.Warn("EventBridge: Events() channel full, dropping event",
				"type", appEvt.Type)
		}

		// Deliver to matching waiters.
		b.mu.Lock()
		for _, w := range b.waiters {
			if w.matches(appEvt.Type) {
				// Non-blocking send — waiter channel has cap 1.
				// If already full, the event is dropped (caller
				// only wants the first matching event).
				select {
				case w.ch <- appEvt:
				default:
				}
			}
		}
		b.mu.Unlock()
	}
}

// Events returns the channel that receives all translated events.
func (b *EventBridge) Events() <-chan Event {
	return b.out
}

// RegisterWaiter registers a wait caller with an optional event type filter.
// Returns a channel to receive the matching event and a cancel function
// that deregisters the waiter.
func (b *EventBridge) RegisterWaiter(filter []string) (ch <-chan Event, cancel func()) {
	w := &waiter{
		filter: make(map[string]struct{}, len(filter)),
		ch:     make(chan Event, 1),
	}
	for _, f := range filter {
		w.filter[f] = struct{}{}
	}

	b.mu.Lock()
	b.waiters = append(b.waiters, w)
	b.mu.Unlock()

	cancel = func() {
		b.mu.Lock()
		defer b.mu.Unlock()
		for i, candidate := range b.waiters {
			if candidate == w {
				b.waiters = append(b.waiters[:i], b.waiters[i+1:]...)
				break
			}
		}
	}
	return w.ch, cancel
}

// Close cancels the bridge context and waits for the goroutine to exit.
func (b *EventBridge) Close() {
	b.cancel()
	<-b.done
}

// translateDomainEvent maps a domain.Event to an Event per FR-033.
func translateDomainEvent(evt domain.Event) Event {
	switch evt.(type) {
	case domain.MessageEvent:
		return Event{Type: "message", Payload: evt}
	case domain.ReceiptEvent:
		return Event{Type: "receipt", Payload: evt}
	case domain.ConnectionEvent:
		return Event{Type: "status", Payload: evt}
	case domain.PairingEvent:
		return Event{Type: "pairing", Payload: evt}
	default:
		return Event{Type: "unknown", Payload: evt}
	}
}
