package domain

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"
)

// A caller waiting on the first call gets that call's failure too, and is
// released with ErrIdempotencyInterrupted when the first call panics.
func TestIdempotencyFlights_FollowerSharesFailureAndSurvivesPanic(t *testing.T) {
	var g IdempotencyFlights
	var h [32]byte
	boom := errors.New("boom")
	for _, c := range []struct {
		name string
		fn   func() ([]byte, error)
		want error
	}{
		{"error", func() ([]byte, error) { return nil, boom }, boom},
		{"panic", func() ([]byte, error) { panic("x") }, ErrIdempotencyInterrupted},
	} {
		running, release, got := make(chan struct{}), make(chan struct{}), make(chan error, 1)
		go func() {
			defer func() { _ = recover() }()
			_, _ = g.Do(context.Background(), "send", "p", c.name, h, func() ([]byte, error) {
				close(running)
				<-release
				return c.fn()
			})
		}()
		<-running
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			_, err := g.Do(ctx, "send", "p", c.name, h, func() ([]byte, error) { return []byte("own"), nil })
			got <- err
		}()
		<-time.After(50 * time.Millisecond)
		close(release)
		if err := <-got; !errors.Is(err, c.want) {
			t.Errorf("%s: follower got %v, want %v", c.name, err, c.want)
		} else if c.name == "panic" && !errors.Is(err, ErrSendUncertain) {
			t.Errorf("panic: follower got %v, want the uncertain outcome (the wire call is unknown)", err)
		}
	}
}

// A flight is scoped to (method, profile, key): the same key on another
// method or profile never joins it and runs its own call.
func TestIdempotencyFlights_ScopeIsMethodProfileKey(t *testing.T) {
	var g IdempotencyFlights
	var h [32]byte
	running, release := make(chan struct{}), make(chan struct{})
	go func() {
		_, _ = g.Do(context.Background(), "send", "p1", "k", h, func() ([]byte, error) {
			close(running)
			<-release
			return []byte("first"), nil
		})
	}()
	<-running
	defer close(release)
	for _, c := range []struct{ method, profile string }{{"sendMedia", "p1"}, {"send", "p2"}} {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		out, err := g.Do(ctx, c.method, c.profile, "k", h, func() ([]byte, error) { return []byte("own"), nil })
		cancel()
		if err != nil || string(out) != "own" {
			t.Errorf("%s/%s joined another scope's flight: %q %v", c.method, c.profile, out, err)
		}
	}
}

// A follower stops waiting when its own ctx ends, with that ctx's error.
func TestIdempotencyFlights_FollowerStopsWithItsOwnCtx(t *testing.T) {
	var g IdempotencyFlights
	var h [32]byte
	running, release := make(chan struct{}), make(chan struct{})
	go func() {
		_, _ = g.Do(context.Background(), "send", "p", "k", h, func() ([]byte, error) {
			close(running)
			<-release
			return nil, nil
		})
	}()
	<-running
	defer close(release)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	got := make(chan error, 1)
	go func() {
		_, err := g.Do(ctx, "send", "p", "k", h, func() ([]byte, error) { return []byte("own"), nil })
		got <- err
	}()
	select {
	case err := <-got:
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("follower err %v, want its own DeadlineExceeded", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("the follower kept waiting after its own ctx ended")
	}
}

// parked starts a leader for the key that blocks until release is closed and
// returns once it holds the claim.
func parked(t *testing.T, g *IdempotencyFlights, ret func() ([]byte, error)) (release func()) {
	t.Helper()
	var h [32]byte
	running, rel := make(chan struct{}), make(chan struct{})
	go func() {
		_, _ = g.Do(context.Background(), "send", "p", "k", h, func() ([]byte, error) {
			close(running)
			<-rel
			return ret()
		})
	}()
	<-running
	return func() { close(rel) }
}

// The wait for a first call that is still running ends in ErrIdempotencyInFlight.
func TestIdempotencyFlights_WaitExpiresAsInFlight(t *testing.T) {
	old := idempotencyFlightWait
	idempotencyFlightWait = 30 * time.Millisecond
	t.Cleanup(func() { idempotencyFlightWait = old })
	var g IdempotencyFlights
	release := parked(t, &g, func() ([]byte, error) { return nil, nil })
	defer release()
	var h [32]byte
	_, err := g.Do(context.Background(), "send", "p", "k", h, func() ([]byte, error) { return []byte("own"), nil })
	if !errors.Is(err, ErrIdempotencyInFlight) {
		t.Fatalf("err %v, want ErrIdempotencyInFlight", err)
	}
}

// Only one follower parks per key: the next is answered at once, so same-key
// retries cannot pile up behind a long send.
func TestIdempotencyFlights_OneFollowerPerKey(t *testing.T) {
	var g IdempotencyFlights
	var h [32]byte
	release := parked(t, &g, func() ([]byte, error) { return []byte("first"), nil })
	first := make(chan string, 1)
	go func() {
		out, _ := g.Do(context.Background(), "send", "p", "k", h, func() ([]byte, error) { return []byte("own"), nil })
		first <- string(out)
	}()
	for { // wait until the first follower holds the one waiting place
		g.mu.Lock()
		w := g.m["send\x00p\x00k"].waiting
		g.mu.Unlock()
		if w {
			break
		}
		<-time.After(time.Millisecond)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if _, err := g.Do(ctx, "send", "p", "k", h, func() ([]byte, error) { return []byte("own"), nil }); !errors.Is(err, ErrIdempotencyInFlight) {
		t.Fatalf("second follower: %v, want ErrIdempotencyInFlight at once", err)
	}
	release()
	if got := <-first; got != "first" {
		t.Errorf("parked follower got %q, want the first call's result", got)
	}
}

// A first call that ended with its own ctx error says nothing about a
// follower whose ctx is alive: the follower runs the call itself, once.
func TestIdempotencyFlights_FollowerReRunsAfterLeaderCtxError(t *testing.T) {
	var g IdempotencyFlights
	var h [32]byte
	release := parked(t, &g, func() ([]byte, error) { return nil, context.Canceled })
	got := make(chan string, 1)
	go func() {
		out, err := g.Do(WithFlightRerun(context.Background()), "send", "p", "k", h, func() ([]byte, error) { return []byte("own"), nil })
		got <- string(out) + fmt.Sprint(err)
	}()
	<-time.After(50 * time.Millisecond)
	release()
	if r := <-got; r != "own<nil>" {
		t.Errorf("follower got %q, want its own run", r)
	}
}

// A first call that ended with a ctx error AFTER the wire write is
// send_uncertain (its cause is the ctx error): the follower must get that
// uncertain result, never run the call again.
func TestIdempotencyFlights_NoRerunAfterAnUncertainLeader(t *testing.T) {
	var g IdempotencyFlights
	var h [32]byte
	release := parked(t, &g, func() ([]byte, error) {
		return nil, NewSendUncertain("WAID-1", context.Canceled)
	})
	type res struct {
		err  error
		runs int
	}
	got := make(chan res, 1)
	go func() {
		runs := 0
		_, err := g.Do(WithFlightRerun(context.Background()), "send", "p", "k", h, func() ([]byte, error) { runs++; return []byte("own"), nil })
		got <- res{err, runs}
	}()
	<-time.After(50 * time.Millisecond)
	release()
	r := <-got
	var u *SendUncertainError
	if r.runs != 0 || !errors.As(r.err, &u) || u.MessageID != "WAID-1" {
		t.Fatalf("follower: runs=%d err=%v, want the leader's send_uncertain WAID-1 and no second run", r.runs, r.err)
	}
}

// L2 (p8): only the send class (its ctx carries WithFlightRerun) may re-run
// after a leader's ctx error, because only it types a post-wire ctx end as
// send_uncertain. Another keyed method (group.create) may have written its
// request: its follower gets the leader's error and the call does not run again.
func TestIdempotencyFlights_NoRerunForAMethodThatIsNotSendClass(t *testing.T) {
	var g IdempotencyFlights
	var h [32]byte
	release := parked(t, &g, func() ([]byte, error) { return nil, context.Canceled })
	type res struct {
		err  error
		runs int
	}
	got := make(chan res, 1)
	go func() {
		runs := 0
		_, err := g.Do(context.Background(), "send", "p", "k", h, func() ([]byte, error) { runs++; return []byte("own"), nil })
		got <- res{err, runs}
	}()
	<-time.After(50 * time.Millisecond)
	release()
	if r := <-got; r.runs != 0 || !errors.Is(r.err, context.Canceled) {
		t.Fatalf("follower: runs=%d err=%v, want the leader's context.Canceled and no second run", r.runs, r.err)
	}
}

// L1 (p8): a parked follower that stops waiting (its ctx ends, or the wait
// expires) frees the one waiting place: the next retry parks, it is not turned
// away with -32024 until the first call ends.
func TestIdempotencyFlights_LeavingFollowerFreesTheWaitingPlace(t *testing.T) {
	for _, how := range []string{"ctx", "timeout"} {
		t.Run(how, func(t *testing.T) {
			var g IdempotencyFlights
			var h [32]byte
			release := parked(t, &g, func() ([]byte, error) { return []byte("first"), nil })
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if how == "timeout" {
				old := idempotencyFlightWait
				idempotencyFlightWait = 20 * time.Millisecond
				t.Cleanup(func() { idempotencyFlightWait = old })
			}
			left := make(chan error, 1)
			go func() {
				_, err := g.Do(ctx, "send", "p", "k", h, nil)
				left <- err
			}()
			if how == "ctx" {
				for { // until it is parked
					g.mu.Lock()
					w := g.m["send\x00p\x00k"].waiting
					g.mu.Unlock()
					if w {
						break
					}
					<-time.After(time.Millisecond)
				}
				cancel()
			}
			if err := <-left; err == nil {
				t.Fatal("the first follower should have left with an error")
			}
			idempotencyFlightWait = 2 * time.Minute
			next := make(chan error, 1)
			go func() {
				_, err := g.Do(context.Background(), "send", "p", "k", h, nil)
				next <- err
			}()
			select {
			case err := <-next:
				t.Fatalf("next retry returned %v at once; it should park behind the first call", err)
			case <-time.After(50 * time.Millisecond):
			}
			release()
			if err := <-next; err != nil {
				t.Errorf("parked retry: %v, want the first call's result", err)
			}
		})
	}
}

// Observe is told each outcome as a closed set of names, never a key.
// idle waits until no flight is registered: a finished leader removes its claim
// just after its caller returns, and a new leader must not race that.
func idle(g *IdempotencyFlights) {
	for {
		g.mu.Lock()
		n := len(g.m)
		g.mu.Unlock()
		if n == 0 {
			return
		}
		<-time.After(time.Millisecond)
	}
}

func TestIdempotencyFlights_ObservesEveryResult(t *testing.T) {
	var mu sync.Mutex
	seen := map[string]int{}
	g := IdempotencyFlights{Observe: func(r string) { mu.Lock(); seen[r]++; mu.Unlock() }}
	var h, other [32]byte
	other[0] = 1
	release := parked(t, &g, func() ([]byte, error) { return nil, context.Canceled })
	var wg sync.WaitGroup
	wg.Add(1)
	go func() { // parks, then re-runs after the leader's ctx error
		defer wg.Done()
		_, _ = g.Do(WithFlightRerun(context.Background()), "send", "p", "k", h, func() ([]byte, error) { return nil, nil })
	}()
	for { // until it holds the one waiting place
		g.mu.Lock()
		w := g.m["send\x00p\x00k"].waiting
		g.mu.Unlock()
		if w {
			break
		}
		<-time.After(time.Millisecond)
	}
	_, _ = g.Do(context.Background(), "send", "p", "k", h, nil)     // turned away
	_, _ = g.Do(context.Background(), "send", "p", "k", other, nil) // collision
	release()
	wg.Wait()
	for r, want := range map[string]int{"leader": 2, "in_flight": 1, "collision": 1, "rerun": 1} {
		if seen[r] != want {
			t.Errorf("%s observed %d times, want %d (all %v)", r, seen[r], want, seen)
		}
	}

	old := idempotencyFlightWait
	idempotencyFlightWait = 20 * time.Millisecond
	t.Cleanup(func() { idempotencyFlightWait = old })
	idle(&g)
	seen = map[string]int{}
	release = parked(t, &g, func() ([]byte, error) { return []byte("x"), nil })
	_, _ = g.Do(context.Background(), "send", "p", "k", h, nil) // wait expires
	release()
	if seen["in_flight"] != 1 || seen["follower"] != 0 {
		t.Errorf("wait expiry: %v, want one in_flight", seen)
	}
	idempotencyFlightWait = old
	idle(&g)
	seen = map[string]int{}
	release = parked(t, &g, func() ([]byte, error) { return []byte("x"), nil })
	done := make(chan struct{})
	go func() { _, _ = g.Do(context.Background(), "send", "p", "k", h, nil); close(done) }()
	<-time.After(30 * time.Millisecond)
	release()
	<-done
	if seen["follower"] != 1 {
		t.Errorf("shared result: %v, want one follower", seen)
	}
}
