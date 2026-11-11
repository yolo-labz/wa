package app

// Fixes for wF:p8's review of the executor hardening (M1-M3, L1, L5);
// the reproductions are p8's, renamed into this package's scheme.

import (
	"context"
	"encoding/json"
	"errors"
	"runtime"
	"strings"
	"testing"
	"testing/synctest"
	"time"
)

func finishedJob(t *testing.T, e *Executor, owner string) string {
	t.Helper()
	f := newFake(0)
	f.free()
	id := runAsync(t, e, owner, f).JobID
	synctest.Wait()
	return id
}

func tombCount(e *Executor) (tombs, execs int) {
	e.mu.Lock()
	defer e.mu.Unlock()
	return len(e.tombs), len(e.execs)
}

// TestSyncJobs_StaleTombIDsPruned (M1, p8 P8-1): ids forgotten by expiry
// leave the tombstone FIFO; it stays bounded by live entries.
func TestSyncJobs_StaleTombIDsPruned(t *testing.T) {
	executorTest(t, DefaultExecLimits, func(t *testing.T, e *Executor) {
		for range 6 {
			for range 10 {
				finishedJob(t, e, "a")
			}
			<-time.After(DefaultExecLimits.Retention + 2*reapEvery)
			synctest.Wait()
		}
		if tombs, execs := tombCount(e); execs != 0 || tombs != 0 {
			t.Errorf("execs=%d but tombs holds %d dead ids", execs, tombs)
		}
	})
}

// TestSyncJobs_ThreeOwnersCannotFlushVictim (M2, p8 P8-2): past the global
// bound the owner holding the most results loses its oldest, so one
// unfetched result survives three owners holding 32 each. Each lost
// unfetched result is counted; the warning is rate-limited.
func TestSyncJobs_ThreeOwnersCannotFlushVictim(t *testing.T) {
	var logs lockedBuf
	executorTest(t, withLog(&logs), func(t *testing.T, e *Executor) {
		vid := finishedJob(t, e, "victim")
		firstX := ""
		for _, o := range []string{"x", "y", "z"} {
			for i := range ownerRetained {
				if id := finishedJob(t, e, o); o == "x" && i == 0 {
					firstX = id
				}
			}
		}
		wantState(t, e, "victim", vid, StateDone)
		if _, err := e.Get(t.Context(), own("x"), firstX); !errors.Is(err, ErrJobNotFound) {
			t.Errorf("x's oldest result was kept: %v", err)
		}
		lost := int64(1 + 3*ownerRetained - DefaultExecLimits.InFlight - DefaultExecLimits.Jobs)
		if n := e.EvictedUnfetchedTotal(); n != lost {
			t.Errorf("EvictedUnfetchedTotal = %d, want %d", n, lost)
		}
		if n := strings.Count(logs.String(), "unfetched job result evicted"); n != 1 {
			t.Errorf("%d eviction warnings in one burst, want 1", n)
		}
	})
}

// TestSyncJobs_FetchedEvictedBeforeUnfetched (M2): over the global bound,
// fetched or expired tombstones go first, oldest first.
func TestSyncJobs_FetchedEvictedBeforeUnfetched(t *testing.T) {
	executorTest(t, DefaultExecLimits, func(t *testing.T, e *Executor) {
		var unfetched []string
		for _, o := range []string{"x", "a", "y"} {
			for range ownerRetained {
				id := finishedJob(t, e, o)
				if o == "a" {
					wantState(t, e, o, id, StateDone) // fetched
					continue
				}
				unfetched = append(unfetched, id)
			}
		}
		if n := e.EvictedUnfetchedTotal(); n != 0 {
			t.Fatalf("%d unfetched results evicted while fetched ones remained", n)
		}
		for i, id := range unfetched {
			wantState(t, e, []string{"x", "y"}[i/ownerRetained], id, StateDone)
		}
	})
}

// TestSafety_WedgedFlightReplacedAfterLifetime (M3): a safety worker that
// never returns is joined only within its lifetime; after it, one call
// starts a replacement flight, which later calls join.
func TestSafety_WedgedFlightReplacedAfterLifetime(t *testing.T) {
	executorTest(t, DefaultExecLimits, func(t *testing.T, e *Executor) {
		wedge := make(chan struct{})
		defer close(wedge)
		hung := func(context.Context) (json.RawMessage, error) { <-wedge; return nil, nil }
		if _, err := e.Run(t.Context(), "o", "panic", false, hung); !errors.Is(err, ErrExecDeadline) {
			t.Fatalf("wedged call: %v", err)
		}
		early := newFake(0)
		early.free()
		if _, err := e.Run(t.Context(), "o", "panic", false, early.run); !errors.Is(err, ErrExecDeadline) || early.calls.Load() != 0 {
			t.Fatalf("within the lifetime: err=%v ran=%d, want a join", err, early.calls.Load())
		}
		<-time.After(MethodDeadlines["panic"].MaxWorker)
		slow, ok := newFake(0), newFake(0)
		ok.free()
		go func() { _, _ = e.Run(context.Background(), "o", "panic", false, slow.run) }()
		synctest.Wait()
		go func() { _, _ = e.Run(context.Background(), "o", "panic", false, ok.run) }()
		synctest.Wait()
		if slow.calls.Load() != 1 || ok.calls.Load() != 0 {
			t.Fatalf("replacement: slow ran %d, second ran %d; want one replacement, then a join", slow.calls.Load(), ok.calls.Load())
		}
		slow.free()
		synctest.Wait()
		fresh := newFake(0)
		fresh.free()
		if _, err := e.Run(t.Context(), "o", "panic", false, fresh.run); err != nil || fresh.calls.Load() != 1 {
			t.Errorf("after the replacement ended: err=%v ran=%d", err, fresh.calls.Load())
		}
	})
}

// TestSafety_GoexitReleasesFlight (L1, p8 P8-3): runtime.Goexit in a
// safety worker releases the method; the next call runs.
func TestSafety_GoexitReleasesFlight(t *testing.T) {
	executorTest(t, DefaultExecLimits, func(t *testing.T, e *Executor) {
		_, err := e.Run(t.Context(), "o", "panic", false, func(context.Context) (json.RawMessage, error) {
			runtime.Goexit()
			return nil, nil
		})
		if !errors.Is(err, ErrInternal) {
			t.Errorf("Goexit worker answered %v, want ErrInternal", err)
		}
		ok := newFake(0)
		ok.free()
		if _, err := e.Run(t.Context(), "o", "panic", false, ok.run); err != nil || ok.calls.Load() != 1 {
			t.Errorf("next kill switch call: err=%v ran=%d", err, ok.calls.Load())
		}
	})
}

// TestSyncJobs_GoexitReleasesSlot (L1): runtime.Goexit in a job worker
// fails the job and frees its slot.
func TestSyncJobs_GoexitReleasesSlot(t *testing.T) {
	executorTest(t, DefaultExecLimits, func(t *testing.T, e *Executor) {
		raw, err := e.Run(t.Context(), "a", "sync.force", true, func(context.Context) (json.RawMessage, error) {
			runtime.Goexit()
			return nil, nil
		})
		r := decode(t, raw, err)
		synctest.Wait()
		wantCounts(t, e, 0, 0)
		if v := wantState(t, e, "a", r.JobID, StateFailed); !errors.Is(v.Err, ErrInternal) {
			t.Errorf("Goexit job error %v, want ErrInternal", v.Err)
		}
	})
}

// TestRun_SyncCancelLeavesNoTombstone (L5): nobody can poll a synchronous
// execution, so its caller leaving leaves nothing behind once it exits.
func TestRun_SyncCancelLeavesNoTombstone(t *testing.T) {
	executorTest(t, DefaultExecLimits, func(t *testing.T, e *Executor) {
		for _, honour := range []bool{true, false} {
			f := newFake(time.Second)
			f.honourCtx = honour
			ctx, cancel := context.WithCancel(context.Background())
			go func() { _, _ = e.Run(ctx, "a", "status", false, f.run) }()
			synctest.Wait()
			cancel()
			<-time.After(2 * time.Second)
			synctest.Wait()
			if tombs, execs := tombCount(e); tombs != 0 || execs != 0 {
				t.Errorf("worker honours ctx=%v: %d tombstones, %d entries", honour, tombs, execs)
			}
		}
	})
}
