package domain

import (
	"context"
	"fmt"
	"sync"
	"time"
)

// IdempotencyFlightWait bounds how long a second caller of a keyed call
// waits for the first one (the longest a promoted send may run).
const IdempotencyFlightWait = 2 * time.Minute

// IdempotencyFlights lets one call per (method, profile, key) run at a time.
// The claim is taken before the call starts, so a retry that arrives during
// a send whose caller hung up joins it instead of sending a second message.
// The zero value is ready; both IdempotencyStore implementations embed it.
type IdempotencyFlights struct {
	mu sync.Mutex
	m  map[string]*idempotencyFlight
}

type idempotencyFlight struct {
	hash [32]byte
	done chan struct{}
	out  []byte
	err  error
}

// Do runs fn as the flight for the key, or waits for the flight already
// running and returns its result (ErrIdempotencyCollision when the other
// call's params differ). The wait ends with ctx or IdempotencyFlightWait.
func (g *IdempotencyFlights) Do(ctx context.Context, method, profile, key string, hash [32]byte, fn func() ([]byte, error)) ([]byte, error) {
	id := method + "\x00" + profile + "\x00" + key
	g.mu.Lock()
	if f, ok := g.m[id]; ok {
		g.mu.Unlock()
		if f.hash != hash {
			return nil, fmt.Errorf("%w: method=%s key=%s", ErrIdempotencyCollision, method, key)
		}
		t := time.NewTimer(IdempotencyFlightWait)
		defer t.Stop()
		select {
		case <-f.done:
			return append([]byte(nil), f.out...), f.err
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-t.C:
			return nil, fmt.Errorf("idempotency: the first call with key %s is still running", key)
		}
	}
	f := &idempotencyFlight{hash: hash, done: make(chan struct{}), err: ErrIdempotencyInterrupted}
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
