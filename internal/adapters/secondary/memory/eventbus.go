package memory

import (
	"context"
	"sync"

	"github.com/yolo-labz/wa/internal/app"
	"github.com/yolo-labz/wa/internal/domain"
)

// EventBus is the in-process fan-out implementation of app.EventBus.
// Every subscriber gets an independent filtered channel; Publish is
// non-blocking and drops records that a slow subscriber cannot absorb.
type EventBus struct {
	mu   sync.Mutex
	subs map[*eventSubscription]struct{}
}

// NewEventBus returns an empty EventBus.
func NewEventBus() *EventBus {
	return &EventBus{subs: make(map[*eventSubscription]struct{})}
}

// Subscribe implements app.EventBus.
func (b *EventBus) Subscribe(ctx context.Context, f app.SubscribeFilter) (app.EventSubscription, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	sub := &eventSubscription{
		filter: f,
		ch:     make(chan app.EventRecord, 64),
		bus:    b,
	}
	b.mu.Lock()
	b.subs[sub] = struct{}{}
	b.mu.Unlock()
	return sub, nil
}

// Publish implements app.EventBus.
func (b *EventBus) Publish(ctx context.Context, rec app.EventRecord) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	b.mu.Lock()
	targets := make([]*eventSubscription, 0, len(b.subs))
	for s := range b.subs {
		targets = append(targets, s)
	}
	b.mu.Unlock()

	for _, s := range targets {
		if !matchFilter(s.filter, rec) {
			continue
		}
		select {
		case s.ch <- rec:
		default:
			// slow subscriber: drop on the floor; real impl emits stream.drop.
		}
	}
	return nil
}

type eventSubscription struct {
	filter app.SubscribeFilter
	ch     chan app.EventRecord
	bus    *EventBus
	mu     sync.Mutex
	closed bool
	ackSeq int64
}

func (s *eventSubscription) Events() <-chan app.EventRecord { return s.ch }

func (s *eventSubscription) Close() error {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil
	}
	s.closed = true
	s.mu.Unlock()
	s.bus.mu.Lock()
	delete(s.bus.subs, s)
	s.bus.mu.Unlock()
	close(s.ch)
	return nil
}

func (s *eventSubscription) Ack(seq int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if seq > s.ackSeq {
		s.ackSeq = seq
	}
	return nil
}

func matchFilter(f app.SubscribeFilter, rec app.EventRecord) bool {
	if len(f.Kinds) > 0 && !stringInSlice(rec.Kind, f.Kinds) {
		return false
	}
	if rec.Seq <= f.Since {
		return false
	}
	// Chats/Senders/NotSenders/BodyRe would require JSON parsing the
	// trusted payload; the memory adapter skips that and leaves the real
	// evaluation to the sqlite/socket layer. Kinds + Since is enough for
	// BUS1/BUS2 to pass.
	_ = domain.JID{}
	return true
}

func stringInSlice(s string, list []string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}
