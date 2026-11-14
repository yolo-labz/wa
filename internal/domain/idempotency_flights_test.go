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
