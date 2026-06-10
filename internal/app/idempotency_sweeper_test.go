package app_test

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/yolo-labz/wa/v2/internal/app"
)

type fakeSweepStore struct {
	mu      sync.Mutex
	calls   int
	n       int
	err     error
	befores []time.Time
}

func (s *fakeSweepStore) Sweep(_ context.Context, before time.Time) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls++
	s.befores = append(s.befores, before)
	return s.n, s.err
}

func (s *fakeSweepStore) callCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.calls
}

func (s *fakeSweepStore) firstBefore() time.Time {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.befores) == 0 {
		return time.Time{}
	}
	return s.befores[0]
}

// TestIdempotencySweeper_TicksEveryFiveMinutes pins FR-034a: the loop
// sweeps on a 5-minute cadence (not before) and passes the tick time as
// the eviction horizon. Virtual clock via testing/synctest.
func TestIdempotencySweeper_TicksEveryFiveMinutes(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		store := &fakeSweepStore{n: 3}
		start := time.Now()
		done := make(chan struct{})
		go func() {
			app.RunIdempotencySweeper(ctx, store, slog.New(slog.DiscardHandler))
			close(done)
		}()

		<-time.After(4 * time.Minute)
		synctest.Wait()
		if got := store.callCount(); got != 0 {
			t.Fatalf("calls after 4m = %d, want 0", got)
		}

		<-time.After(time.Minute)
		synctest.Wait()
		if got := store.callCount(); got != 1 {
			t.Fatalf("calls after 5m = %d, want 1", got)
		}
		if want := start.Add(5 * time.Minute); !store.firstBefore().Equal(want) {
			t.Errorf("sweep horizon = %v, want tick time %v", store.firstBefore(), want)
		}

		<-time.After(10 * time.Minute)
		synctest.Wait()
		if got := store.callCount(); got != 3 {
			t.Fatalf("calls after 15m = %d, want 3", got)
		}

		cancel()
		<-done
	})
}

// TestIdempotencySweeper_ErrorContinues pins the resilience contract: a
// failing Sweep logs and keeps the loop alive for the next tick.
func TestIdempotencySweeper_ErrorContinues(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		store := &fakeSweepStore{err: errors.New("disk gone")}
		done := make(chan struct{})
		go func() {
			app.RunIdempotencySweeper(ctx, store, slog.New(slog.DiscardHandler))
			close(done)
		}()

		<-time.After(10 * time.Minute)
		synctest.Wait()
		if got := store.callCount(); got != 2 {
			t.Fatalf("calls = %d, want 2 (errors must not stop the loop)", got)
		}

		cancel()
		<-done
	})
}

// TestIdempotencySweeper_CancelStops pins shutdown: ctx cancellation
// returns promptly. Also covers the nil-logger default branch (n=0 and
// no error, so the defaulted logger never writes).
func TestIdempotencySweeper_CancelStops(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		store := &fakeSweepStore{}
		done := make(chan struct{})
		go func() {
			app.RunIdempotencySweeper(ctx, store, nil)
			close(done)
		}()

		<-time.After(5 * time.Minute)
		synctest.Wait()
		if got := store.callCount(); got != 1 {
			t.Fatalf("calls = %d, want 1", got)
		}

		cancel()
		<-done
		if got := store.callCount(); got != 1 {
			t.Errorf("calls after cancel = %d, want still 1", got)
		}
	})
}
