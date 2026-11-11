package app

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"testing/synctest"
	"time"
)

// lockedBuf is a log sink the worker goroutine and the test both touch.
type lockedBuf struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (l *lockedBuf) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.Write(p)
}
func (l *lockedBuf) String() string { l.mu.Lock(); defer l.mu.Unlock(); return l.b.String() }

func withLog(l *lockedBuf) ExecLimits {
	lim := DefaultExecLimits
	lim.Log = slog.New(slog.NewTextHandler(l, nil))
	return lim
}

// TestExecutor_PanicTextNotReturned (review F5): a recovered panic is
// logged with its stack; the caller, sync or via jobs.get, sees only
// ErrInternal.
func TestExecutor_PanicTextNotReturned(t *testing.T) {
	var logs lockedBuf
	executorTest(t, withLog(&logs), func(t *testing.T, e *Executor) {
		boom := func(_ context.Context) (json.RawMessage, error) { panic("db password=hunter2 at /srv/wa/profile.db") }
		_, err := e.Run(t.Context(), "tok", "status", false, boom)
		if !errors.Is(err, ErrInternal) || strings.Contains(err.Error(), "hunter2") {
			t.Fatalf("sync caller sees %v, want ErrInternal without the panic text", err)
		}
		raw, err := e.Run(t.Context(), "tok", "sync.force", true, boom)
		id := decode(t, raw, err).JobID
		synctest.Wait()
		v, err := e.Get(t.Context(), own("tok"), id)
		if err != nil || !errors.Is(v.Err, ErrInternal) || strings.Contains(v.Err.Error(), "hunter2") {
			t.Fatalf("job result %+v, %v: want ErrInternal without the panic text", v, err)
		}
		if out := logs.String(); !strings.Contains(out, "hunter2") || !strings.Contains(out, "goroutine") {
			t.Fatalf("panic and stack not logged:\n%s", out)
		}
	})
}

// TestExecutor_SafetyMethodSingleFlight (N1 ruling): panic and the two
// logouts take no target, so concurrent calls of one method join the
// in-flight run instead of spawning another; methods stay separate, and
// a call after the flight ended runs again.
func TestExecutor_SafetyMethodSingleFlight(t *testing.T) {
	executorTest(t, DefaultExecLimits, func(t *testing.T, e *Executor) {
		type got struct {
			raw json.RawMessage
			err error
		}
		var mu sync.Mutex
		var results []got
		call := func(owner, method string, f *fake) {
			go func() {
				raw, err := e.Run(t.Context(), owner, method, false, f.run)
				mu.Lock()
				results = append(results, got{raw, err})
				mu.Unlock()
			}()
		}
		panics, logout, logoutAll := newFake(0), newFake(0), newFake(0)
		for i := range 100 {
			call("tok"+string(rune('a'+i%7)), "panic", panics)
		}
		call("ownerA", "session.logout", logout)
		call("ownerB", "session.logout", logout)
		call("ownerA", "session.logoutAll", logoutAll)
		synctest.Wait()
		for name, f := range map[string]*fake{"panic": panics, "session.logout": logout, "session.logoutAll": logoutAll} {
			if n := f.calls.Load(); n != 1 {
				t.Errorf("%s ran %d times while in flight, want 1", name, n)
			}
			f.free()
		}
		synctest.Wait()
		mu.Lock()
		if len(results) != 103 {
			t.Fatalf("%d callers answered, want 103", len(results))
		}
		for _, r := range results {
			if r.err != nil || string(r.raw) != `{"n":1}` {
				t.Fatalf("joiner got %s, %v; want the shared result", r.raw, r.err)
			}
		}
		mu.Unlock()
		again := newFake(0)
		again.free()
		if r := runSync(t, e, "tokz", "panic", again); r.N != 1 || again.calls.Load() != 1 {
			t.Errorf("a call after the flight ended did not run: %+v", r)
		}
	})
}

// TestExecutor_StuckWorkerReported (review F3): a worker that ignores
// cancellation past twice its deadline is counted and logged once; a
// worker that exits in time is not. Nothing is killed or freed.
func TestExecutor_StuckWorkerReported(t *testing.T) {
	var logs lockedBuf
	executorTest(t, withLog(&logs), func(t *testing.T, e *Executor) {
		quick := newFake(time.Second)
		runSync(t, e, "tok", "status", quick)
		hung, hungSafety := newFake(0), newFake(0)
		raw, err := e.Run(t.Context(), "tok", "send", false, hung.run)
		decode(t, raw, err)
		if _, err := e.Run(t.Context(), "tok", "panic", false, hungSafety.run); !errors.Is(err, ErrExecDeadline) {
			t.Fatalf("hung panic: %v", err)
		}
		// Both calls have returned by t=20 s: the job's worker started at t=0,
		// the safety method's at t=10 s.
		life := MethodDeadlines["send"].MaxWorker
		<-time.After(2*life - 2*ReturnDeadline - time.Second)
		if n := e.StuckWorkers(); n != 0 {
			t.Fatalf("%d stuck before twice the deadline", n)
		}
		<-time.After(2 * time.Second)
		synctest.Wait()
		if n := e.StuckWorkers(); n != 1 {
			t.Fatalf("StuckWorkers = %d at 2x the job's deadline, want 1", n)
		}
		<-time.After(ReturnDeadline)
		synctest.Wait()
		if n := e.StuckWorkers(); n != 2 {
			t.Fatalf("StuckWorkers = %d, want 2 (the job and the safety method)", n)
		}
		if out := logs.String(); !strings.Contains(out, "method=send") || !strings.Contains(out, "method=panic") {
			t.Fatalf("stuck workers not logged by method:\n%s", out)
		}
		hung.free()
		synctest.Wait()
		if n := e.StuckWorkers(); n != 2 {
			t.Errorf("counter changed to %d when the worker finally exited", n)
		}
	})
}

// TestSyncJobs_FinishedJobFreesSlot (review F4): a finished job gives its
// pool slot back at worker exit, still pollable.
func TestSyncJobs_FinishedJobFreesSlot(t *testing.T) {
	executorTest(t, DefaultExecLimits, func(t *testing.T, e *Executor) {
		ids := make([]string, 0, DefaultExecLimits.JobsPer+1)
		for range DefaultExecLimits.JobsPer {
			f := newFake(0)
			f.free()
			ids = append(ids, runAsync(t, e, "a", f).JobID)
		}
		synctest.Wait()
		wantCounts(t, e, 0, 0)
		extra := newFake(0)
		extra.free()
		ids = append(ids, runAsync(t, e, "a", extra).JobID) // fails today with ErrExecCapacity
		synctest.Wait()
		for _, id := range ids {
			wantState(t, e, "a", id, StateDone)
		}
	})
}

// TestSyncJobs_OwnerRetentionBounded: one owner finishing many jobs
// cannot push another owner's unfetched result out of the tombstone FIFO.
func TestSyncJobs_OwnerRetentionBounded(t *testing.T) {
	executorTest(t, DefaultExecLimits, func(t *testing.T, e *Executor) {
		other := newFake(0)
		other.free()
		keep := runAsync(t, e, "b", other).JobID
		var first string
		for i := range 100 {
			f := newFake(0)
			f.free()
			id := runAsync(t, e, "a", f).JobID
			if i == 0 {
				first = id
			}
			synctest.Wait()
		}
		wantState(t, e, "b", keep, StateDone)
		if _, err := e.Get(t.Context(), own("a"), first); !errors.Is(err, ErrJobNotFound) {
			t.Errorf("owner a's oldest retained result survived the per-owner cap: %v", err)
		}
		e.mu.Lock()
		n := 0
		for _, x := range e.execs {
			if x.owner == "a" {
				n++
			}
		}
		e.mu.Unlock()
		if n > ownerRetained {
			t.Errorf("owner a retains %d finished jobs, cap %d", n, ownerRetained)
		}
	})
}

// TestSyncJobs_ResultCapped: an async/promoted result over 1 MiB is
// replaced by a marker; small results and a synchronous result are not.
func TestSyncJobs_ResultCapped(t *testing.T) {
	executorTest(t, DefaultExecLimits, func(t *testing.T, e *Executor) {
		big := `{"p":"` + strings.Repeat("x", 2<<20) + `"}`
		f := newFake(0)
		f.result = big
		f.free()
		id := runAsync(t, e, "a", f).JobID
		synctest.Wait()
		v := wantState(t, e, "a", id, StateDone)
		var m struct {
			Truncated bool `json:"truncated"`
			Bytes     int  `json:"bytes"`
			Limit     int  `json:"limit"`
		}
		if err := json.Unmarshal(v.Result, &m); err != nil || !m.Truncated || m.Bytes != len(big) || m.Limit != 1<<20 {
			t.Fatalf("async result %d bytes, marker %+v, %v", len(v.Result), m, err)
		}
		small := newFake(0)
		small.free()
		id = runAsync(t, e, "a", small).JobID
		synctest.Wait()
		if v := wantState(t, e, "a", id, StateDone); string(v.Result) != `{"n":1}` {
			t.Errorf("small result altered: %s", v.Result)
		}
		sync := newFake(0)
		sync.result = big
		sync.free()
		raw, err := e.Run(t.Context(), "a", "send", false, sync.run)
		if err != nil || len(raw) != len(big) {
			t.Errorf("synchronous result cut: %d bytes, %v", len(raw), err)
		}
	})
}
