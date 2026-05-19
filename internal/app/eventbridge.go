package app

import (
	"context"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/yolo-labz/wa/v2/internal/domain"
	"github.com/yolo-labz/wa/v2/internal/observability"
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

const (
	// eventChannelBuffer is the capacity of the Events() output channel.
	eventChannelBuffer = 64
	// eventStreamErrorBackoff is the delay before retrying after a
	// stream error. Chosen to prevent tight-loop on transient failures.
	eventStreamErrorBackoff = 100 * time.Millisecond
)

// EventBridge reads from the pull-based EventStream port and fans out
// events to both the Events() channel and registered wait waiters.
type EventBridge struct {
	stream  EventStream
	out     chan Event
	log     *slog.Logger
	profile string

	mu      sync.Mutex
	waiters []*waiter

	// lastEventUnix holds the unix-time of the most recent event the
	// bridge observed. Feature 018 T3-06: `wa health` reports this so
	// an operator can eyeball whether the subscribe pipeline is live.
	lastEventUnix atomic.Int64

	ctx    context.Context
	cancel context.CancelFunc
	done   chan struct{}
}

// LastEventUnix returns the unix-time of the most recent event the
// bridge observed, or 0 if no event has arrived yet. Safe for
// concurrent callers.
func (b *EventBridge) LastEventUnix() int64 { return b.lastEventUnix.Load() }

// SetProfile records the active profile name on the bridge so every
// span it opens carries wa.profile. Safe to call once after
// construction; later calls during Run are racy and forbidden.
func (b *EventBridge) SetProfile(p string) { b.profile = p }

// NewEventBridge creates an event bridge that reads from the given
// EventStream. Call Run() to start the bridge goroutine.
func NewEventBridge(stream EventStream, log *slog.Logger) *EventBridge {
	if log == nil {
		log = slog.Default()
	}
	ctx, cancel := context.WithCancel(context.Background())
	return &EventBridge{
		stream: stream,
		out:    make(chan Event, eventChannelBuffer),
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
			case <-time.After(eventStreamErrorBackoff):
			case <-b.ctx.Done():
				return
			}
			continue
		}

		appEvt := translateDomainEvent(evt)
		b.dispatch(appEvt, true /* updateLastEvent */)
	}
}

// dispatch is the shared fan-out path used by Run() for upstream events
// and by EmitSynthetic for cmd/wad-originated events (spec 110g).
// updateLastEvent is true for real upstream events (they bump the
// staleness clock) and false for synthetic watchdog signals (they MUST
// NOT bump the clock — otherwise emitting softStale would reset the
// very metric the next tick is supposed to read).
func (b *EventBridge) dispatch(appEvt Event, updateLastEvent bool) {
	if updateLastEvent {
		b.lastEventUnix.Store(time.Now().Unix())
	}

	// FR-035: one wa.subscribe PRODUCER span per notification
	// delivered. The span covers the fan-out work (main channel
	// push + waiter fan-out), ending once all snapshot waiters
	// have been contacted.
	_, span := observability.StartSubscribeNotification(b.ctx, b.profile, appEvt.Type)

	// Push to the main Events() channel (non-blocking).
	select {
	case b.out <- appEvt:
	default:
		b.log.Warn("EventBridge: Events() channel full, dropping event",
			"type", appEvt.Type)
	}
	observability.GetMetrics().SetEventQueueDepth(len(b.out))

	// Deliver to matching waiters using copy-under-lock pattern
	// (Watermill/NATS consensus): copy the slice under lock,
	// release, then iterate without holding mu.  This prevents
	// any ordering issue if a waiter cancel func fires during
	// fan-out from a different goroutine.
	b.mu.Lock()
	snapshot := make([]*waiter, len(b.waiters))
	copy(snapshot, b.waiters)
	b.mu.Unlock()

	for _, w := range snapshot {
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
	span.End()
}

// EmitSynthetic publishes a domain.Event produced inside the app/cmd-wad
// layer (not by the upstream whatsmeow stream). Spec 110g — the soft-stale
// watchdog uses this to surface ConnectivityHealthEvent{softStale} and
// ConnectivityHealthEvent{restored} signals through the same subscribe
// fan-out that real events take, so subscribers see them on
// `state.softStale` and `state.restored` event kinds. Synthetic events
// do NOT bump LastEventUnix — see dispatch.
func (b *EventBridge) EmitSynthetic(evt domain.Event) {
	if evt == nil {
		return
	}
	b.dispatch(translateDomainEvent(evt), false)
}

// Events returns the channel that receives all translated events.
func (b *EventBridge) Events() <-chan Event {
	return b.out
}

// RegisterWaiter registers a wait caller with an optional event type filter.
// Returns a channel to receive the matching event and a cancel function
// that deregisters the waiter.
func (b *EventBridge) RegisterWaiter(filter []string) (ch <-chan Event, cancel func()) {
	return b.registerSubscriber(filter, 1)
}

// SubscribeStream registers a long-lived event subscriber for the
// SSE primary adapter (spec 110b). bufSize controls the per-
// subscriber channel buffer — under load, slow consumers drop the
// oldest events when the buffer fills (matches the socket adapter's
// CodeStreamDrop semantics, FR-063). filter is the same shape as
// RegisterWaiter — empty means "all events".
//
// Pass bufSize=256 unless you have a specific reason; 256 covers
// typical inbound burst rates with substantial headroom.
func (b *EventBridge) SubscribeStream(filter []string, bufSize int) (ch <-chan Event, cancel func()) {
	if bufSize < 1 {
		bufSize = 1
	}
	return b.registerSubscriber(filter, bufSize)
}

// registerSubscriber is the shared implementation for RegisterWaiter
// (cap=1, one-shot) and SubscribeStream (cap=N, long-lived). The
// fan-out loop in Run() does a non-blocking send on each waiter
// channel — full channels drop, mirroring the socket adapter's
// stream_drop behaviour.
func (b *EventBridge) registerSubscriber(filter []string, bufSize int) (ch <-chan Event, cancel func()) {
	w := &waiter{
		filter: make(map[string]struct{}, len(filter)),
		ch:     make(chan Event, bufSize),
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
// Spec 110g adds ConnectivityHealthEvent — each State value becomes its
// own `state.<camelCase>` event kind so subscribers can filter precisely
// (e.g. wait only on `state.softStale`).
func translateDomainEvent(evt domain.Event) Event {
	switch e := evt.(type) {
	case domain.MessageEvent:
		return Event{Type: "message", Payload: evt}
	case domain.ReceiptEvent:
		return Event{Type: "receipt", Payload: evt}
	case domain.ConnectionEvent:
		return Event{Type: "status", Payload: evt}
	case domain.PairingEvent:
		return Event{Type: "pairing", Payload: evt}
	case domain.ConnectivityHealthEvent:
		return Event{Type: "state." + e.State.String(), Payload: evt}
	default:
		return Event{Type: "unknown", Payload: evt}
	}
}
