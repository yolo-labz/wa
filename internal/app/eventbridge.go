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
	// eventStreamErrorBackoffMax caps the exponential growth of the
	// retry delay under consecutive stream errors (SF-08). The ceiling
	// trades log volume against event-resume latency: a permanently
	// broken stream logs once per 5s instead of 10×/s, while a stream
	// that heals mid-backoff delays event delivery by at most 5s.
	eventStreamErrorBackoffMax = 5 * time.Second
)

// nextStreamBackoff doubles the retry delay up to the ceiling. Pure so
// the growth curve is unit-testable without driving real sleeps.
func nextStreamBackoff(cur time.Duration) time.Duration {
	next := cur * 2
	if next > eventStreamErrorBackoffMax {
		return eventStreamErrorBackoffMax
	}
	return next
}

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

	// lastTrafficUnix holds the unix-time of the most recent ORGANIC
	// traffic event (message/receipt). Connection-status churn does not
	// count — in particular the events whatsmeow emits during a
	// watchdog-forced reconnect. The soft-stale recover backoff (spec
	// 110g backoff extension) reads this clock to tell "the link carried
	// real traffic since the last forced reconnect" apart from "only our
	// own reconnect churn", which lastEventUnix cannot distinguish.
	lastTrafficUnix atomic.Int64

	// streamErrors counts upstream stream.Next failures since boot;
	// lastStreamErrUnix stamps the most recent one. Together they are
	// the operator-visible "subscribe pipeline degraded" signal (SF-08)
	// surfaced through wa health — before, a permanently-failing stream
	// was invisible outside the daemon log.
	streamErrors      atomic.Int64
	lastStreamErrUnix atomic.Int64

	ctx    context.Context
	cancel context.CancelFunc
	done   chan struct{}
}

// LastEventUnix returns the unix-time of the most recent event the
// bridge observed, or 0 if no event has arrived yet. Safe for
// concurrent callers.
func (b *EventBridge) LastEventUnix() int64 { return b.lastEventUnix.Load() }

// LastTrafficUnix returns the unix-time of the most recent organic
// traffic event (message/receipt) the bridge observed, or 0 if none has
// arrived yet. Connection-status and synthetic events never advance this
// clock. Safe for concurrent callers.
func (b *EventBridge) LastTrafficUnix() int64 { return b.lastTrafficUnix.Load() }

// StreamErrors returns the total number of upstream stream read
// failures since boot. Safe for concurrent callers.
func (b *EventBridge) StreamErrors() int64 { return b.streamErrors.Load() }

// LastStreamErrorUnix returns the unix-time of the most recent stream
// read failure, or 0 if none has occurred. Safe for concurrent callers.
func (b *EventBridge) LastStreamErrorUnix() int64 { return b.lastStreamErrUnix.Load() }

// isTrafficEvent reports whether an event type counts as organic inbound
// traffic for LastTrafficUnix. message and receipt both originate from
// the WhatsApp event-routing path that a zombie link silently drops;
// status/pairing originate from the local connection lifecycle and fire
// on every forced reconnect, so counting them would defeat the backoff.
func isTrafficEvent(eventType string) bool {
	return eventType == "message" || eventType == "receipt"
}

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

	backoff := eventStreamErrorBackoff
	for {
		evt, err := b.stream.Next(b.ctx)
		if err != nil {
			if b.ctx.Err() != nil {
				return // shutdown
			}
			b.streamErrors.Add(1)
			b.lastStreamErrUnix.Store(time.Now().Unix())
			b.log.Error("EventBridge: stream error, retrying",
				"error", err,
				"backoff", backoff,
				"streamErrors", b.streamErrors.Load())
			// Backoff before retry per FR-035; consecutive errors grow
			// the delay exponentially to the SF-08 ceiling so a
			// permanently broken stream cannot flood the log at 10×/s.
			select {
			case <-time.After(backoff):
			case <-b.ctx.Done():
				return
			}
			backoff = nextStreamBackoff(backoff)
			continue
		}
		backoff = eventStreamErrorBackoff

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
		now := time.Now().Unix()
		b.lastEventUnix.Store(now)
		if isTrafficEvent(appEvt.Type) {
			b.lastTrafficUnix.Store(now)
		}
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
		// SEC-02 / FR-005a: subscribers must never see raw attacker
		// text — fold it into the <channel> envelope projection here,
		// the single app-layer choke point for every bridge consumer.
		return Event{Type: "message", Payload: wrapMessageEventForSubscribers(e)}
	case domain.EditEvent:
		// Type stays "unknown" for wire-compat (EditEvent had no kind
		// mapping before SEC-02); the payload is wrapped regardless.
		return Event{Type: "unknown", Payload: wrapEditEventForSubscribers(e)}
	case domain.ReceiptEvent:
		return Event{Type: "receipt", Payload: evt}
	case domain.ConnectionEvent:
		return Event{Type: "status", Payload: evt}
	case domain.PairingEvent:
		return Event{Type: "pairing", Payload: evt}
	case domain.ConnectivityHealthEvent:
		return Event{Type: "state." + e.State.String(), Payload: evt}
	case domain.MediaTranscribedEvent:
		return Event{Type: "media.transcribed", Payload: evt}
	default:
		return Event{Type: "unknown", Payload: evt}
	}
}
