package domain

import (
	"context"
	"errors"
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
