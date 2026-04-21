package app_test

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/yolo-labz/wa/internal/app"
	"github.com/yolo-labz/wa/internal/domain"
)

// fakeScheduledStore satisfies app.ScheduledStore in-memory. Used only by
// runner tests — the real sqlite adapter has its own suite.
type fakeScheduledStore struct {
	mu   sync.Mutex
	rows map[string]domain.ScheduledSend
}

func newFakeScheduledStore() *fakeScheduledStore {
	return &fakeScheduledStore{rows: make(map[string]domain.ScheduledSend)}
}

func fakeKey(profile, id string) string { return profile + "|" + id }

func (s *fakeScheduledStore) Put(_ context.Context, ss domain.ScheduledSend) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.rows[fakeKey(ss.Profile(), ss.ID())] = ss
	return nil
}

func (s *fakeScheduledStore) Update(_ context.Context, ss domain.ScheduledSend) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.rows[fakeKey(ss.Profile(), ss.ID())]; !ok {
		return errors.New("not found")
	}
	s.rows[fakeKey(ss.Profile(), ss.ID())] = ss
	return nil
}

func (s *fakeScheduledStore) Get(_ context.Context, profile, id string) (domain.ScheduledSend, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	ss, ok := s.rows[fakeKey(profile, id)]
	if !ok {
		return domain.ScheduledSend{}, errors.New("not found")
	}
	return ss, nil
}

func (s *fakeScheduledStore) ListPending(_ context.Context, profile string) ([]domain.ScheduledSend, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]domain.ScheduledSend, 0)
	for _, ss := range s.rows {
		if ss.Profile() == profile && ss.State() == domain.SchedulePending {
			out = append(out, ss)
		}
	}
	return out, nil
}

func (s *fakeScheduledStore) List(_ context.Context, profile string, state domain.ScheduledState, _ int) ([]domain.ScheduledSend, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]domain.ScheduledSend, 0)
	for _, ss := range s.rows {
		if ss.Profile() != profile {
			continue
		}
		if state != 0 && ss.State() != state {
			continue
		}
		out = append(out, ss)
	}
	return out, nil
}

func (s *fakeScheduledStore) Delete(_ context.Context, profile, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.rows, fakeKey(profile, id))
	return nil
}

func mustPendingFor(t *testing.T, id string, fireAt, now time.Time) domain.ScheduledSend {
	t.Helper()
	jid, err := domain.Parse("5511999999999")
	if err != nil {
		t.Fatalf("parse jid: %v", err)
	}
	ss, err := domain.NewScheduledSend(id, "default", domain.ScheduleKindSendText, jid,
		"hi", "", fireAt, now)
	if err != nil {
		t.Fatalf("NewScheduledSend: %v", err)
	}
	return ss
}

// TestScheduleFiresWithin5s is the SC-08 accuracy check: a schedule armed
// for fireAt = now+5s must fire at or before now+5s (±5 s window). Uses
// testing/synctest for deterministic virtual time.
func TestScheduleFiresWithin5s(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		store := newFakeScheduledStore()
		var fired atomic.Int32
		done := make(chan struct{}, 1)
		runner := app.NewScheduleRunner(store, "default",
			func(_ context.Context, _, _ string) {
				fired.Add(1)
				select {
				case done <- struct{}{}:
				default:
				}
			})

		start := time.Now()
		fire := start.Add(5 * time.Second)
		if err := store.Put(ctx, mustPendingFor(t, "sch-1", fire, start)); err != nil {
			t.Fatalf("Put: %v", err)
		}
		if err := runner.Start(ctx); err != nil {
			t.Fatalf("Start: %v", err)
		}

		// Advance past fireAt.
		time.Sleep(5100 * time.Millisecond)
		synctest.Wait()

		if fired.Load() != 1 {
			t.Fatalf("fire callback invoked %d times, want 1", fired.Load())
		}
		// Verify timer dropped from the armed set.
		if n := runner.Pending(); n != 0 {
			t.Fatalf("Pending()=%d want 0 after fire", n)
		}
	})
}

// TestSchedulePastDueFiresImmediately covers the resume-after-restart
// branch: rows whose fireAt is already in the past arm with delay=0 and
// fire on the next scheduler tick.
func TestSchedulePastDueFiresImmediately(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		store := newFakeScheduledStore()
		var fired atomic.Int32
		runner := app.NewScheduleRunner(store, "default",
			func(_ context.Context, _, _ string) { fired.Add(1) })

		now := time.Now()
		ss := mustPendingFor(t, "late", now.Add(time.Hour), now)
		// Mutate the stored row's fireAt into the past via Reschedule-style
		// workaround: use HydrateScheduledSend directly.
		past := domain.HydrateScheduledSend(ss.ID(), ss.Profile(), ss.Kind(), ss.Recipient(),
			ss.Body(), ss.MediaPath(), now.Add(-10*time.Minute), now.Add(-2*time.Hour),
			domain.SchedulePending, now.Add(-2*time.Hour), "")
		if err := store.Put(ctx, past); err != nil {
			t.Fatalf("Put: %v", err)
		}

		if err := runner.Start(ctx); err != nil {
			t.Fatalf("Start: %v", err)
		}
		synctest.Wait()

		if fired.Load() != 1 {
			t.Fatalf("fire count=%d want 1", fired.Load())
		}
	})
}

// TestScheduleCancelStopsTimer ensures schedule.cancel wiring drops the
// pending timer before it would fire.
func TestScheduleCancelStopsTimer(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		store := newFakeScheduledStore()
		var fired atomic.Int32
		runner := app.NewScheduleRunner(store, "default",
			func(_ context.Context, _, _ string) { fired.Add(1) })

		start := time.Now()
		fire := start.Add(10 * time.Second)
		ss := mustPendingFor(t, "sch-cancel", fire, start)
		if err := store.Put(ctx, ss); err != nil {
			t.Fatalf("Put: %v", err)
		}
		if err := runner.Start(ctx); err != nil {
			t.Fatalf("Start: %v", err)
		}

		// Cancel before fireAt.
		time.Sleep(3 * time.Second)
		runner.Cancel("default", "sch-cancel")

		// Advance past original fireAt.
		time.Sleep(10 * time.Second)
		synctest.Wait()

		if fired.Load() != 0 {
			t.Fatalf("fire invoked %d times after cancel, want 0", fired.Load())
		}
		if n := runner.Pending(); n != 0 {
			t.Fatalf("Pending()=%d want 0", n)
		}
	})
}

// TestScheduleReschedulesReplacesTimer verifies that calling Schedule a
// second time for the same id replaces the prior timer (no double-fire).
func TestScheduleReschedulesReplacesTimer(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		store := newFakeScheduledStore()
		var fired atomic.Int32
		runner := app.NewScheduleRunner(store, "default",
			func(_ context.Context, _, _ string) { fired.Add(1) })
		if err := runner.Start(ctx); err != nil {
			t.Fatalf("Start: %v", err)
		}

		start := time.Now()
		orig := mustPendingFor(t, "sch-r", start.Add(5*time.Second), start)
		runner.Schedule(orig)

		later, err := orig.Reschedule(start.Add(20*time.Second), start)
		if err != nil {
			t.Fatalf("Reschedule: %v", err)
		}
		runner.Schedule(later)

		time.Sleep(7 * time.Second)
		synctest.Wait()
		if fired.Load() != 0 {
			t.Fatalf("fired early: %d", fired.Load())
		}

		time.Sleep(15 * time.Second)
		synctest.Wait()
		if fired.Load() != 1 {
			t.Fatalf("fire count=%d want 1 (no double-fire)", fired.Load())
		}
	})
}
