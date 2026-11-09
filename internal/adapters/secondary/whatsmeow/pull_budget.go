package whatsmeow

import (
	"context"
	"errors"
	"slices"
	"sync"
	"time"
)

// FR-116-24 daemon-wide pull budget: at most 2 concurrent pulls and 10 starts
// per rolling minute across watchdog, recovery and explicit force sweeps.
// Waiters queue fairly, one entry per chat, capped at 64; a chat already
// queued mints no second pull.
const (
	maxConcurrentPulls  = 2
	maxPullStartsPerMin = 10
	maxPullQueue        = 64
)

var (
	errPullQueued = errors.New("whatsmeow: a pull for this chat is already queued")
	errPullBudget = errors.New("whatsmeow: pull budget exhausted")
)

type pullBudget struct {
	mu      sync.Mutex
	active  int
	starts  []time.Time
	queue   []string
	changed chan struct{} // closed and replaced on every state change
}

// admissibleLocked prunes the rolling window and reports whether a pull may
// start now, or how long until the window frees a slot.
func (b *pullBudget) admissibleLocked(now time.Time) (bool, time.Duration) {
	cut := now.Add(-time.Minute)
	i := 0
	for i < len(b.starts) && !b.starts[i].After(cut) {
		i++
	}
	b.starts = b.starts[i:]
	if b.active >= maxConcurrentPulls {
		return false, 0
	}
	if len(b.starts) >= maxPullStartsPerMin {
		return false, b.starts[0].Sub(cut)
	}
	return true, 0
}

func (b *pullBudget) signalLocked() {
	if b.changed != nil {
		close(b.changed)
	}
	b.changed = make(chan struct{})
}

func (b *pullBudget) startLocked(now time.Time) func() {
	b.active++
	b.starts = append(b.starts, now)
	var once sync.Once
	return func() {
		once.Do(func() {
			b.mu.Lock()
			b.active--
			b.signalLocked()
			b.mu.Unlock()
		})
	}
}

// try admits at once or refuses; it never queues and never jumps the queue.
func (b *pullBudget) try() (func(), bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	now := time.Now()
	if ok, _ := b.admissibleLocked(now); !ok || len(b.queue) > 0 {
		return nil, false
	}
	return b.startLocked(now), true
}

// acquire waits its FIFO turn; cancellation leaves the queue.
func (b *pullBudget) acquire(ctx context.Context, chat string) (func(), error) {
	b.mu.Lock()
	if slices.Contains(b.queue, chat) {
		b.mu.Unlock()
		return nil, errPullQueued
	}
	if len(b.queue) >= maxPullQueue {
		b.mu.Unlock()
		return nil, errPullBudget
	}
	b.queue = append(b.queue, chat)
	for {
		now := time.Now()
		ok, retry := b.admissibleLocked(now)
		if ok && b.queue[0] == chat {
			b.queue = b.queue[1:]
			release := b.startLocked(now)
			b.signalLocked()
			b.mu.Unlock()
			return release, nil
		}
		if b.changed == nil {
			b.changed = make(chan struct{})
		}
		changed := b.changed
		b.mu.Unlock()
		if err := waitTurn(ctx, changed, retry); err != nil {
			b.mu.Lock()
			b.queue = slices.DeleteFunc(b.queue, func(c string) bool { return c == chat })
			b.signalLocked()
			b.mu.Unlock()
			return nil, err
		}
		b.mu.Lock()
	}
}

func waitTurn(ctx context.Context, changed <-chan struct{}, retry time.Duration) error {
	var expired <-chan time.Time
	if retry > 0 {
		t := time.NewTimer(retry)
		defer t.Stop()
		expired = t.C
	}
	select {
	case <-changed:
	case <-expired:
	case <-ctx.Done():
		return ctx.Err()
	}
	return nil
}
