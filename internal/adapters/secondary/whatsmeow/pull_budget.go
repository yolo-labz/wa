package whatsmeow

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sync"
	"time"

	"github.com/yolo-labz/wa/v2/internal/app"
)

// FR-116-24 daemon-wide pull budget: at most 2 concurrent pulls and 10 starts
// per rolling minute across watchdog, recovery and explicit force sweeps.
// Waiters queue fairly, one entry per chat, capped at 64; a chat already
// queued mints no second pull.
const (
	maxConcurrentPulls  = 2
	maxPullStartsPerMin = 10
	maxPullQueue        = 64

	// Per-caller share of the daemon budget: one scoped token cannot take
	// more, so it cannot starve another caller or the sweep. The daemon caps
	// above still apply on top.
	maxCallerConcurrentPulls  = 1
	maxCallerPullStartsPerMin = 4
)

var (
	errPullQueued = errors.New("whatsmeow: a pull for this chat is already queued")
	errPullBudget = errors.New("whatsmeow: pull budget exhausted")
)

// callerBudgetError is a caller exceeding its own share: refused at once,
// never queued behind others and never dropped silently. RetryAfter is the
// time until its oldest start leaves the minute; zero means its previous pull
// still holds the slot and nothing honest can be promised (it can wait minutes).
type callerBudgetError struct{ RetryAfter time.Duration }

func (e *callerBudgetError) Error() string {
	if e.RetryAfter <= 0 {
		return "whatsmeow: caller pull budget exhausted: its previous pull is still running"
	}
	return fmt.Sprintf("whatsmeow: caller pull budget exhausted; retry in %s", e.RetryAfter.Round(time.Second))
}

type callerState struct {
	active int
	starts []time.Time
}

type pullBudget struct {
	mu      sync.Mutex
	active  int
	starts  []time.Time
	queue   []string
	callers map[string]*callerState
	changed chan struct{} // closed and replaced on every state change
}

// callerKey is the sub-budget key: the scoped token's principal. No principal
// (recovery, watchdog) and Admin principals (the owner socket, the single env
// token) stay on the daemon pool alone.
func callerKey(ctx context.Context) string {
	if p, ok := app.CallerFrom(ctx); ok && !p.Admin {
		return p.ID
	}
	return ""
}

// admitCallerLocked reserves one of who's slots, or says when to retry. It
// also forgets callers with nothing active and no start inside the window.
func (b *pullBudget) admitCallerLocked(who string, now time.Time) *callerBudgetError {
	cut := now.Add(-time.Minute)
	for k, c := range b.callers {
		c.starts = slices.DeleteFunc(c.starts, func(t time.Time) bool { return !t.After(cut) })
		if c.active == 0 && len(c.starts) == 0 {
			delete(b.callers, k)
		}
	}
	c := b.callers[who]
	if c == nil {
		if b.callers == nil {
			b.callers = map[string]*callerState{}
		}
		c = &callerState{}
		b.callers[who] = c
	}
	if c.active >= maxCallerConcurrentPulls {
		return &callerBudgetError{}
	}
	if len(c.starts) >= maxCallerPullStartsPerMin {
		return &callerBudgetError{RetryAfter: c.starts[0].Sub(cut)}
	}
	c.active++ // the start is counted when the pull starts (startLocked), not here
	return nil
}

func (b *pullBudget) releaseCallerLocked(who string) {
	if c := b.callers[who]; c != nil {
		c.active--
	}
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

func (b *pullBudget) startLocked(now time.Time, who string) func() {
	b.active++
	b.starts = append(b.starts, now)
	if c := b.callers[who]; c != nil {
		c.starts = append(c.starts, now)
	}
	return func() { // callers release exactly once
		b.mu.Lock()
		b.active--
		b.releaseCallerLocked(who)
		b.signalLocked()
		b.mu.Unlock()
	}
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
	who := callerKey(ctx)
	if who != "" {
		if cerr := b.admitCallerLocked(who, time.Now()); cerr != nil {
			b.mu.Unlock()
			return nil, cerr
		}
	}
	b.queue = append(b.queue, chat)
	for {
		now := time.Now()
		ok, retry := b.admissibleLocked(now)
		if ok && b.queue[0] == chat {
			b.queue = b.queue[1:]
			release := b.startLocked(now, who)
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
			b.releaseCallerLocked(who)
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
