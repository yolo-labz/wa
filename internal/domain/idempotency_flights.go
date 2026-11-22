package domain

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"
)

// idempotencyFlightWait bounds how long a second caller of a keyed call waits
// for the first one (the longest a promoted send may run). A var so a test can
// shrink it.
var idempotencyFlightWait = 2 * time.Minute

// IdempotencyFlights lets one call per (method, profile, key) run at a time.
// The claim is taken before the call starts, so a retry that arrives during
// a send whose caller hung up joins it instead of sending a second message.
// The zero value is ready; both IdempotencyStore implementations embed it.
type IdempotencyFlights struct {
	mu sync.Mutex
	m  map[string]*idempotencyFlight
}

type idempotencyFlight struct {
	hash    [32]byte
	done    chan struct{}
	waiting bool // a follower is already parked: one per key
	out     []byte
	err     error
}

// Do runs fn as the flight for the key, or waits for the flight already
// running and returns its result (ErrIdempotencyCollision when the other
// call's params differ). Only one follower parks per key; any further one is
// answered ErrIdempotencyInFlight at once, so same-key retries cannot hold the
// caller's in-flight slots. The wait ends with ctx or idempotencyFlightWait
// (ErrIdempotencyInFlight). A first call that panics leaves the waiter
// ErrSendUncertain (cause ErrIdempotencyInterrupted, no id): whether the wire
// call happened is unknown, so the waiter must not resend. A first call that
// ended with its own ctx error while the follower's ctx is alive says nothing
// about the follower, so the follower runs once itself, unless that
// first call is send_uncertain (the frame may be out: never run again).
func (g *IdempotencyFlights) Do(ctx context.Context, method, profile, key string, hash [32]byte, fn func() ([]byte, error)) ([]byte, error) {
	id := method + "\x00" + profile + "\x00" + key
	for attempt := 0; ; attempt++ {
		g.mu.Lock()
		f, ok := g.m[id]
		if !ok {
			return g.lead(id, hash, fn)
		}
		if f.hash != hash {
			g.mu.Unlock()
			return nil, fmt.Errorf("%w: method=%s key=%s", ErrIdempotencyCollision, method, key)
		}
		if f.waiting {
			g.mu.Unlock()
			return nil, fmt.Errorf("%w: key %s (a retry is already waiting)", ErrIdempotencyInFlight, key)
		}
		f.waiting = true
		g.mu.Unlock()
		out, err := wait(ctx, f, key)
		if attempt == 0 && ctx.Err() == nil && !errors.Is(err, ErrSendUncertain) && (errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded)) && f.ended() {
			continue
		}
		return out, err
	}
}

func (f *idempotencyFlight) ended() bool {
	select {
	case <-f.done:
		return true
	default:
		return false
	}
}

func wait(ctx context.Context, f *idempotencyFlight, key string) ([]byte, error) {
	t := time.NewTimer(idempotencyFlightWait)
	defer t.Stop()
	select {
	case <-f.done:
		return append([]byte(nil), f.out...), f.err
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-t.C:
		return nil, fmt.Errorf("%w: key %s", ErrIdempotencyInFlight, key)
	}
}

// lead runs fn as the flight; g.mu is held on entry and released here.
func (g *IdempotencyFlights) lead(id string, hash [32]byte, fn func() ([]byte, error)) ([]byte, error) {
	f := &idempotencyFlight{hash: hash, done: make(chan struct{}), err: NewSendUncertain("", ErrIdempotencyInterrupted)}
	if g.m == nil {
		g.m = make(map[string]*idempotencyFlight)
	}
	g.m[id] = f
	g.mu.Unlock()
	defer func() { // also on a panic or Goexit: followers are always released
		g.mu.Lock()
		delete(g.m, id)
		g.mu.Unlock()
		close(f.done)
	}()
	f.out, f.err = fn()
	return f.out, f.err
}
