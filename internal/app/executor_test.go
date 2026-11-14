package app

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"go.uber.org/goleak"

	"github.com/yolo-labz/wa/v2/internal/domain"
)

// fake is an in-memory execution body. It ignores its context unless
// honourCtx, finishes after delay (fake time) or when release closes,
// and counts invocations.
type fake struct {
	calls     atomic.Int32
	delay     time.Duration
	release   chan struct{}
	honourCtx bool
	result    string
	err       error
	once      sync.Once
}

// fakes records every fake so executorTest can free them all even when a
// test stops early, keeping one failure from deadlocking the bubble.
var (
	fakesMu sync.Mutex
	fakes   []*fake
)

func newFake(delay time.Duration) *fake {
	f := &fake{delay: delay, release: make(chan struct{}), result: `{"n":1}`}
	fakesMu.Lock()
	fakes = append(fakes, f)
	fakesMu.Unlock()
	return f
}

func (f *fake) free() { f.once.Do(func() { close(f.release) }) }

func (f *fake) run(ctx context.Context) (json.RawMessage, error) {
	f.calls.Add(1)
	var ctxDone <-chan struct{}
	if f.honourCtx {
		ctxDone = ctx.Done()
	}
	var timeout <-chan time.Time
	if f.delay > 0 {
		timeout = time.After(f.delay)
	}
	select {
	case <-timeout:
	case <-f.release:
	case <-ctxDone:
		return nil, ctx.Err()
	}
	return json.RawMessage(f.result), f.err
}

type reply struct {
	JobID    string `json:"job_id"`
	State    string `json:"state"`
	TimedOut bool   `json:"timed_out"`
	N        int    `json:"n"`
}

func decode(t *testing.T, raw json.RawMessage, err error) reply {
	t.Helper()
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	var r reply
	if err := json.Unmarshal(raw, &r); err != nil {
		t.Fatalf("reply %s: %v", raw, err)
	}
	return r
}

func runSync(t *testing.T, e *Executor, owner, method string, f *fake) reply {
	t.Helper()
	raw, err := e.Run(t.Context(), owner, method, false, f.run)
	return decode(t, raw, err)
}

func runAsync(t *testing.T, e *Executor, owner string, f *fake) reply {
	t.Helper()
	raw, err := e.Run(t.Context(), owner, "sync.force", true, f.run)
	return decode(t, raw, err)
}

func (e *Executor) counts() (inFlight, jobs, entries int) {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.count[poolInFlight], e.count[poolJob], len(e.execs)
}

func wantState(t *testing.T, e *Executor, owner, id, state string) JobView {
	t.Helper()
	v, err := e.Get(t.Context(), own(owner), id)
	if err != nil || v.State != state {
		t.Fatalf("get %s = %+v, %v; want state %s", id, v, err, state)
	}
	return v
}

func wantCounts(t *testing.T, e *Executor, inFlight, jobs int) {
	t.Helper()
	if i, j, _ := e.counts(); i != inFlight || j != jobs {
		t.Fatalf("pool counts in-flight %d jobs %d, want %d/%d", i, j, inFlight, jobs)
	}
}

// executorTest runs body in a synctest bubble with an executor that is
// closed, and whose fake workers have exited, before the bubble ends.
func executorTest(t *testing.T, limits ExecLimits, body func(t *testing.T, e *Executor)) {
	t.Helper()
	executorTestWith(t, limits, ExecAuth{}, body)
}

func executorTestWith(t *testing.T, limits ExecLimits, auth ExecAuth, body func(t *testing.T, e *Executor)) {
	t.Helper()
	synctest.Test(t, func(t *testing.T) {
		e := NewExecutor(limits, auth)
		defer func() {
			fakesMu.Lock()
			for _, f := range fakes {
				f.free()
			}
			fakes = nil
			fakesMu.Unlock()
			e.Close()
			synctest.Wait()
		}()
		body(t, e)
	})
}

var roomy = ExecLimits{InFlight: 1000, InFlightPer: 1000, Jobs: 1000, JobsPer: 1000, Retention: time.Hour}

// TestNoMethodBlocksOver10s: every classified method returns at exactly
// its class's deadline (10 s; 45 s for the send class) with a job id when
// its worker never finishes, keeps
// its synchronous shape when it finishes first, and an unclassified
// method is refused without running.
func TestNoMethodBlocksOver10s(t *testing.T) {
	executorTest(t, roomy, func(t *testing.T, e *Executor) {
		methods := make([]string, 0, len(MethodDeadlines))
		for m := range MethodDeadlines {
			methods = append(methods, m)
		}
		sort.Strings(methods)
		stuck := newFake(0)
		for _, m := range methods {
			start := time.Now()
			if uncappedMethods[m] {
				if _, err := e.Run(t.Context(), "op", m, false, stuck.run); !errors.Is(err, ErrExecDeadline) || time.Since(start) != MethodDeadlines[m].Return {
					t.Errorf("%s: %v after %v, want ErrExecDeadline at %v", m, err, time.Since(start), MethodDeadlines[m].Return)
				}
				continue
			}
			r := runSync(t, e, "op", m, stuck)
			if elapsed := time.Since(start); elapsed != MethodDeadlines[m].Return || r.JobID == "" || !r.TimedOut || r.State != StateRunning {
				t.Errorf("%s: returned %+v after %v, want a running job at %v", m, r, elapsed, MethodDeadlines[m].Return)
			}
		}
		if got := int(stuck.calls.Load()); got != len(methods) {
			t.Errorf("%d invocations for %d methods", got, len(methods))
		}
		stuck.free()
		start := time.Now()
		if r := runSync(t, e, "op", "send", newFake(9*time.Second)); r.N != 1 || r.JobID != "" || time.Since(start) != 9*time.Second {
			t.Errorf("fast result %+v: want the synchronous shape", r)
		}
		never := newFake(0)
		if _, err := e.Run(t.Context(), "op", "no.such.method", false, never.run); !errors.Is(err, ErrUnclassifiedMethod) || never.calls.Load() != 0 {
			t.Errorf("unclassified method: %v (calls %d), want refusal before running", err, never.calls.Load())
		}
	})
}

// TestMethodDeadline_IgnoresCancellationReturnsJob: a worker that ignores
// cancellation and finishes at 30 s has a pollable id at 10 s, survives
// its request's cancellation, and yields exactly one result — also when
// promotion has to wait for job quota.
func TestMethodDeadline_IgnoresCancellationReturnsJob(t *testing.T) {
	executorTest(t, DefaultExecLimits, func(t *testing.T, e *Executor) {
		reqCtx, cancelReq := context.WithCancel(t.Context())
		f := newFake(30 * time.Second)
		raw, err := e.Run(reqCtx, "tok", "sync.force", false, f.run)
		r := decode(t, raw, err)
		if r.JobID == "" || r.State != StateRunning || !r.TimedOut {
			t.Fatalf("at 10 s: %+v", r)
		}
		cancelReq() // the request is gone; the job is not
		<-time.After(10 * time.Second)
		wantState(t, e, "tok", r.JobID, StateRunning)
		<-time.After(11 * time.Second)
		if v := wantState(t, e, "tok", r.JobID, StateDone); string(v.Result) != `{"n":1}` {
			t.Errorf("result %s", v.Result)
		}

		// Again with job quota full: capacity_waiting, same id, one result.
		hold := []*fake{newFake(0), newFake(0)}
		for _, h := range hold {
			runAsync(t, e, "tok", h)
		}
		w := runSync(t, e, "tok", "media.download", newFake(30*time.Second))
		if w.State != StateCapacityWaiting || w.JobID == "" {
			t.Fatalf("promotion with quota full: %+v", w)
		}
		<-time.After(21 * time.Second)
		wantState(t, e, "tok", w.JobID, StateDone)
		for _, h := range hold {
			h.free()
		}
		if f.calls.Load() != 1 {
			t.Errorf("worker invoked %d times", f.calls.Load())
		}
	})
}

// TestMethodDeadline_CancelRetainsWorkerSlot: cancelling signals without
// joining; the stubborn worker's slot stays charged until it exits, no
// replacement starts, and its late result is suppressed.
func TestMethodDeadline_CancelRetainsWorkerSlot(t *testing.T) {
	executorTest(t, DefaultExecLimits, func(t *testing.T, e *Executor) {
		a, b := newFake(0), newFake(0)
		ja := runAsync(t, e, "tok", a)
		runAsync(t, e, "tok", b)
		synctest.Wait()
		start := time.Now()
		if v, err := e.Cancel(t.Context(), own("tok"), ja.JobID); err != nil || v.State != StateCancelRequested || time.Since(start) != 0 {
			t.Fatalf("cancel = %+v, %v after %v; want cancellation_requested at once", v, err, time.Since(start))
		}
		third := newFake(0)
		if _, err := e.Run(t.Context(), "tok", "sync.force", true, third.run); !errors.Is(err, ErrExecCapacity) || third.calls.Load() != 0 {
			t.Fatalf("job slot freed before the worker exited: %v", err)
		}
		a.free() // the stubborn worker finally returns a result
		synctest.Wait()
		if v := wantState(t, e, "tok", ja.JobID, StateCancelled); v.Result != nil {
			t.Errorf("late result delivered: %s", v.Result)
		}
		runAsync(t, e, "tok", third)
		if a.calls.Load() != 1 {
			t.Errorf("cancelled worker re-run: %d", a.calls.Load())
		}

		// The in-flight pool behaves the same for a cancelled request.
		e2 := NewExecutor(ExecLimits{InFlight: 1, InFlightPer: 1, Jobs: 1, JobsPer: 1, Retention: time.Hour}, ExecAuth{})
		reqCtx, cancelReq := context.WithCancel(t.Context())
		s := newFake(0)
		go func() { <-time.After(time.Second); cancelReq() }()
		if _, err := e2.Run(reqCtx, "tok", "history", false, s.run); !errors.Is(err, context.Canceled) {
			t.Fatalf("cancelled request: %v", err)
		}
		synctest.Wait()
		if _, err := e2.Run(t.Context(), "tok", "history", false, newFake(time.Second).run); !errors.Is(err, ErrExecCapacity) {
			t.Fatalf("in-flight slot freed before the worker exited: %v", err)
		}
		s.free()
		synctest.Wait()
		runSync(t, e2, "tok", "history", newFake(time.Second))
		e2.Close()
		b.free()
		third.free()
	})
}

// TestMethodDeadline_InFlightSeparateFromJobCaps: ordinary calls use the
// 64/16 in-flight pool, never job quota; 8 concurrent fast calls by one
// principal succeed even with its 2 jobs taken.
func TestMethodDeadline_InFlightSeparateFromJobCaps(t *testing.T) {
	executorTest(t, DefaultExecLimits, func(t *testing.T, e *Executor) {
		h1, h2 := newFake(0), newFake(0)
		runAsync(t, e, "tok", h1)
		runAsync(t, e, "tok", h2)
		var wg sync.WaitGroup
		errs := make(chan error, 8)
		for range 8 {
			wg.Go(func() {
				_, err := e.Run(t.Context(), "tok", "status", false, newFake(time.Second).run)
				errs <- err
			})
		}
		synctest.Wait()
		wantCounts(t, e, 8, 2)
		wg.Wait()
		close(errs)
		for err := range errs {
			if err != nil {
				t.Errorf("fast call: %v", err)
			}
		}
		wantCounts(t, e, 0, 2)

		// 16 per principal, 64 per daemon, refused before running.
		hold := newFake(0)
		for i := range 64 {
			owner := []string{"a", "b", "c", "d"}[i%4]
			wg.Go(func() { _, _ = e.Run(t.Context(), owner, "status", false, hold.run) })
		}
		synctest.Wait()
		over := newFake(0)
		for _, owner := range []string{"a", "e"} {
			if _, err := e.Run(t.Context(), owner, "status", false, over.run); !errors.Is(err, ErrExecCapacity) {
				t.Errorf("owner %s past the in-flight cap: %v", owner, err)
			}
		}
		if over.calls.Load() != 0 {
			t.Error("refused call ran")
		}
		hold.free()
		wg.Wait()
		h1.free()
		h2.free()
	})
}

// TestMethodDeadline_PromotionFullRemainsBounded: with job quota full a
// promoted execution waits charged to in-flight, starts no worker, is
// pollable and cancellable, transfers when quota frees, and a finished
// waiter keeps its in-flight entry until fetched or expired.
func TestMethodDeadline_PromotionFullRemainsBounded(t *testing.T) {
	executorTest(t, DefaultExecLimits, func(t *testing.T, e *Executor) {
		jobs := make([]*fake, 0, 16)
		var first reply
		for i := range 16 {
			f := newFake(0)
			jobs = append(jobs, f)
			r := runAsync(t, e, string(rune('a'+i/2)), f)
			if i == 0 {
				first = r
			}
		}
		w := newFake(0)
		r := runSync(t, e, "x", "sync.force", w)
		if r.State != StateCapacityWaiting {
			t.Fatalf("want capacity_waiting, got %+v", r)
		}
		wantCounts(t, e, 1, 16)
		wantState(t, e, "x", r.JobID, StateCapacityWaiting)
		jobs[0].free()
		synctest.Wait()
		wantState(t, e, "a", first.JobID, StateDone) // fetch frees quota
		wantState(t, e, "x", r.JobID, StateRunning)
		wantCounts(t, e, 0, 16)
		w.free()
		synctest.Wait()
		wantState(t, e, "x", r.JobID, StateDone)

		// A waiter finishing before transfer frees its in-flight slot at
		// exit and stays pollable.
		fill := newFake(0)
		runAsync(t, e, "i", fill)
		done1, done2 := newFake(15*time.Second), newFake(0)
		r1, r2 := runSync(t, e, "y", "history", done1), runSync(t, e, "z", "history", done2)
		if r1.State != StateCapacityWaiting || r2.State != StateCapacityWaiting {
			t.Fatalf("waiters %+v %+v", r1, r2)
		}
		wantCounts(t, e, 1, 16) // done1 finished while z's call waited
		if v, err := e.Cancel(t.Context(), own("z"), r2.JobID); err != nil || v.State != StateCancelRequested {
			t.Fatalf("cancel waiter: %+v %v", v, err)
		}
		wantState(t, e, "y", r1.JobID, StateDone)
		wantCounts(t, e, 1, 16)
		done2.free()
		synctest.Wait()
		wantCounts(t, e, 0, 16)
		if w.calls.Load() != 1 || done1.calls.Load() != 1 {
			t.Error("promotion invoked a worker again")
		}
		for _, f := range append(jobs[1:], fill) {
			f.free()
		}
	})
}

// TestMethodDeadline_NoSelfReexecution: timeouts, polling, request
// cancellation, failure and job cancellation never invoke fn again.
func TestMethodDeadline_NoSelfReexecution(t *testing.T) {
	executorTest(t, DefaultExecLimits, func(t *testing.T, e *Executor) {
		failing := newFake(15 * time.Second)
		failing.err = errors.New("upstream gone")
		reqCtx, cancelReq := context.WithCancel(t.Context())
		raw, err := e.Run(reqCtx, "tok", "history", false, failing.run)
		r := decode(t, raw, err)
		cancelReq()
		for range 10 {
			_, _ = e.Get(t.Context(), own("tok"), r.JobID)
			<-time.After(time.Second)
		}
		if v := wantState(t, e, "tok", r.JobID, StateFailed); v.Err == nil {
			t.Error("failure lost")
		}
		cancelled := newFake(0)
		cancelled.honourCtx = true
		c := runAsync(t, e, "tok", cancelled)
		_, _ = e.Cancel(t.Context(), own("tok"), c.JobID)
		<-time.After(2 * time.Hour)
		if failing.calls.Load() != 1 || cancelled.calls.Load() > 1 {
			t.Errorf("invoked %d and %d times", failing.calls.Load(), cancelled.calls.Load())
		}
	})
}

// TestSyncJobs_Bounded: async admission is capped 16 per daemon / 2 per
// owner before effects, async needs a job method, and the execution
// table stays bounded across many finished jobs.
func TestSyncJobs_Bounded(t *testing.T) {
	executorTest(t, DefaultExecLimits, func(t *testing.T, e *Executor) {
		hold := make([]*fake, 0, 16)
		ids := map[string]string{}
		for i := range 16 {
			f := newFake(0)
			hold = append(hold, f)
			owner := string(rune('a' + i/2))
			ids[runAsync(t, e, owner, f).JobID] = owner
		}
		over := newFake(0)
		for _, owner := range []string{"a", "z"} {
			if _, err := e.Run(t.Context(), owner, "sync.force", true, over.run); !errors.Is(err, ErrExecCapacity) {
				t.Errorf("owner %s: %v, want capacity refusal", owner, err)
			}
		}
		if _, err := e.Run(t.Context(), "z", "send", true, over.run); !errors.Is(err, ErrAsyncNotJob) {
			t.Errorf("async bounded method: %v", err)
		}
		if over.calls.Load() != 0 {
			t.Error("refused admission ran")
		}
		for _, f := range hold {
			f.free()
		}
		synctest.Wait()
		for id, owner := range ids {
			wantState(t, e, owner, id, StateDone)
		}
		for range 500 {
			f := newFake(0)
			f.free()
			id := runAsync(t, e, "a", f).JobID
			synctest.Wait()
			wantState(t, e, "a", id, StateDone)
		}
		limit := DefaultExecLimits.InFlight + 2*DefaultExecLimits.Jobs
		if _, _, n := e.counts(); n > limit {
			t.Errorf("%d retained executions, bound %d", n, limit)
		}
	})
}

// TestSyncJobs_Cancel: cancel is immediate and owner-scoped, a
// cooperative worker ends cancelled with no result, cancelling a
// finished job reports it, and Close cancels every worker.
func TestSyncJobs_Cancel(t *testing.T) {
	executorTest(t, DefaultExecLimits, func(t *testing.T, e *Executor) {
		f := newFake(0)
		f.honourCtx = true
		j := runAsync(t, e, "tok", f)
		for _, try := range [][2]string{{"other", j.JobID}, {"tok", "deadbeef"}} {
			if _, err := e.Cancel(t.Context(), own(try[0]), try[1]); !errors.Is(err, ErrJobNotFound) {
				t.Errorf("cancel %v: %v, want job_not_found", try, err)
			}
		}
		_, _ = e.Cancel(t.Context(), own("tok"), j.JobID)
		synctest.Wait()
		if v := wantState(t, e, "tok", j.JobID, StateCancelled); v.Result != nil || v.Err != nil {
			t.Errorf("cancelled job carries %+v", v)
		}
		quick := newFake(time.Second)
		q := runAsync(t, e, "tok", quick)
		<-time.After(2 * time.Second)
		if v, err := e.Cancel(t.Context(), own("tok"), q.JobID); err != nil || v.State != StateDone {
			t.Errorf("cancel after finish: %+v %v", v, err)
		}

		shut := newFake(0)
		shut.honourCtx = true
		e2 := NewExecutor(DefaultExecLimits, ExecAuth{})
		s := runAsync(t, e2, "tok", shut)
		e2.Close()
		synctest.Wait()
		wantState(t, e2, "tok", s.JobID, StateCancelled)
		if _, err := e2.Run(t.Context(), "tok", "status", false, newFake(0).run); !errors.Is(err, ErrExecCapacity) {
			t.Errorf("admission after Close: %v", err)
		}
	})
}

// TestSyncJobs_ReaperNoLeak: unfetched results expire after 1 h and free
// their slots, expired entries are forgotten later, and Close leaves no
// goroutine behind (goleak, R31).
func TestSyncJobs_ReaperNoLeak(t *testing.T) {
	executorTest(t, DefaultExecLimits, func(t *testing.T, e *Executor) {
		f := newFake(time.Second)
		j := runAsync(t, e, "tok", f)
		<-time.After(time.Second + DefaultExecLimits.Retention - reapEvery)
		wantCounts(t, e, 0, 0) // finished: slot-free, still pollable until retention ends
		<-time.After(2 * reapEvery)
		if v := wantState(t, e, "tok", j.JobID, StateExpired); v.Result != nil {
			t.Errorf("expired job kept its result")
		}
		wantCounts(t, e, 0, 0)
		<-time.After(DefaultExecLimits.Retention + reapEvery)
		if _, err := e.Get(t.Context(), own("tok"), j.JobID); !errors.Is(err, ErrJobNotFound) {
			t.Errorf("expired entry never forgotten: %v", err)
		}
	})
	ignore := goleak.IgnoreCurrent()
	e := NewExecutor(DefaultExecLimits, ExecAuth{})
	if _, err := e.Run(t.Context(), "tok", "status", false, newFake(time.Millisecond).run); err != nil {
		t.Fatal(err)
	}
	e.Close()
	goleak.VerifyNone(t, ignore)
}

// TestExecutor_CallerGoneAtCompletionFreesSlot (review F1): a caller whose
// context ends as the worker finishes leaves nothing charged, whichever
// select arm wins; a leak would exhaust the owner's 16 slots.
func TestExecutor_CallerGoneAtCompletionFreesSlot(t *testing.T) {
	executorTest(t, DefaultExecLimits, func(t *testing.T, e *Executor) {
		instant := newFake(0)
		instant.free()
		for i := range 200 {
			ctx, cancel := context.WithCancel(t.Context())
			cancel() // the client is already gone
			if _, err := e.Run(ctx, "tok", "status", false, instant.run); errors.Is(err, ErrExecCapacity) {
				t.Fatalf("run %d refused: slots leaked", i)
			}
			synctest.Wait()
		}
		for range 50 { // the caller's deadline and the worker end at the same instant
			ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
			_, _ = e.Run(ctx, "tok", "status", false, newFake(5*time.Second).run)
			cancel()
			synctest.Wait()
		}
		wantCounts(t, e, 0, 0)
	})
}

// TestSyncJobs_CancelBeforeStartNeverRunsFn (review F2): a job whose
// cancel lands before its worker starts never invokes fn. The worker is
// driven directly so the ordering is deterministic.
func TestSyncJobs_CancelBeforeStartNeverRunsFn(t *testing.T) {
	executorTest(t, DefaultExecLimits, func(t *testing.T, e *Executor) {
		x := &execution{id: "pre", owner: "tok", pool: poolJob, state: StateQueued, promoted: true, cancel: func() {}, done: make(chan struct{})}
		e.mu.Lock()
		e.execs[x.id] = x
		e.chargeLocked(x, poolJob, 1)
		e.cancelLocked(x)
		e.mu.Unlock()
		never := newFake(0)
		never.free()
		e.work(t.Context(), x, never.run)
		if n := never.calls.Load(); n != 0 {
			t.Fatalf("fn ran %d times after cancellation", n)
		}
		wantState(t, e, "tok", x.id, StateCancelled)
		wantCounts(t, e, 0, 0)
	})
}

// TestExecutor_SafetyMethodsBypassSaturation (review F3 ruling): with every
// slot held, panic and both logouts still run; ordinary calls are refused.
func TestExecutor_SafetyMethodsBypassSaturation(t *testing.T) {
	limits := ExecLimits{InFlight: 2, InFlightPer: 2, Jobs: 1, JobsPer: 1, Retention: time.Hour}
	executorTest(t, limits, func(t *testing.T, e *Executor) {
		hold := newFake(0)
		runAsync(t, e, "tok", hold)
		for range 2 {
			go func() { _, _ = e.Run(t.Context(), "tok", "send", false, hold.run) }()
		}
		synctest.Wait()
		wantCounts(t, e, 2, 1)
		if _, err := e.Run(t.Context(), "tok", "send", false, newFake(0).run); !errors.Is(err, ErrExecCapacity) {
			t.Fatalf("saturated pool admitted an ordinary call: %v", err)
		}
		for _, m := range []string{"panic", "session.logout", "session.logoutAll"} {
			if r := runSync(t, e, "tok", m, newFake(time.Second)); r.N != 1 {
				t.Errorf("%s at saturation: %+v", m, r)
			}
		}
		wantCounts(t, e, 2, 1)
		hold.free()
	})
}

// own is a plain (non-token) principal, as the S4a tests use.
func own(id string) Principal { return Principal{ID: id} }

// TestSendClass_KeepsSynchronousShapeUnder45s (WA-S4C1-SEND-DEADLINE): a send
// that takes 15 s (about 1 in 13 on a personal account; media 13-17 s) answers
// its own result, not {job_id, timed_out}, which a caller would read as the
// answer and resend. Past 45 s the envelope returns, once, with a pollable id;
// a non-send method still returns the envelope at 10 s.
func TestSendClass_KeepsSynchronousShapeUnder45s(t *testing.T) {
	executorTest(t, roomy, func(t *testing.T, e *Executor) {
		for _, m := range sendMethods {
			start := time.Now()
			if r := runSync(t, e, "op", m, newFake(15*time.Second)); r.N != 1 || r.JobID != "" || r.TimedOut || time.Since(start) != 15*time.Second {
				t.Errorf("%s at 15 s: %+v, want the synchronous result", m, r)
			}
			slow := newFake(50 * time.Second)
			start = time.Now()
			r := runSync(t, e, "op", m, slow)
			if r.JobID == "" || !r.TimedOut || r.State != StateRunning || time.Since(start) != SendReturnDeadline {
				t.Errorf("%s at 50 s: %+v after %v, want a running job at %v", m, r, time.Since(start), SendReturnDeadline)
			}
			if wantState(t, e, "op", r.JobID, StateRunning); slow.calls.Load() != 1 {
				t.Errorf("%s ran %d times, want once", m, slow.calls.Load())
			}
			<-time.After(11 * time.Second)
			synctest.Wait()
			if v := wantState(t, e, "op", r.JobID, StateDone); !strings.Contains(string(v.Result), `"n":1`) {
				t.Errorf("%s job result %s", m, v.Result)
			}
		}
		start := time.Now()
		if r := runSync(t, e, "op", "history", newFake(15*time.Second)); r.JobID == "" || !r.TimedOut || time.Since(start) != ReturnDeadline {
			t.Errorf("a non-send method at 15 s: %+v, want the envelope at %v", r, ReturnDeadline)
		}
	})
}

// TestMethodDeadlines_SendClassIsExactlyTheSendHandlers: the longer deadline
// is an allowlist of bounded methods; every other method keeps 10 s.
func TestMethodDeadlines_SendClassIsExactlyTheSendHandlers(t *testing.T) {
	// Pinned independently of sendMethods: dropping a wire-sending method from
	// the class must fail here, not silently restore the 10 s envelope.
	pinned := []string{
		"draft.approve", "media.upload", "message.edit", "message.forward", "message.revoke", "poll.create",
		"poll.vote", "react", "send", "send.buttonResponse", "send.listResponse", "send.reply", "sendMedia",
	}
	if got := slices.Sorted(slices.Values(sendMethods)); !slices.Equal(got, pinned) {
		t.Errorf("send class = %v, want %v", got, pinned)
	}
	send := map[string]bool{}
	for _, m := range sendMethods {
		send[m] = true
		if d := MethodDeadlines[m]; d.Class != Bounded || d.MaxWorker <= SendReturnDeadline {
			t.Errorf("%s: %+v, want a bounded method whose worker outlives the return deadline", m, d)
		}
	}
	for m, d := range MethodDeadlines {
		if want := map[bool]time.Duration{true: SendReturnDeadline, false: ReturnDeadline}[send[m]]; d.Return != want {
			t.Errorf("%s returns at %v, want %v", m, d.Return, want)
		}
	}
	for _, m := range []string{"schedule.send", "markRead", "sendSeen", "chat.composing", "wait", "panic"} {
		if send[m] {
			t.Errorf("%s must not be send class", m)
		}
	}
}

// TestSendClass_HangUpDoesNotCancelTheSend (p8 L3): a client that hangs up
// between 10 and 45 s must not cancel a send mid-wire; it would never learn
// whether the message went out. The send runs to its result, which its owner
// polls. Any other method is still cancelled with its request.
func TestSendClass_HangUpDoesNotCancelTheSend(t *testing.T) {
	executorTest(t, roomy, func(t *testing.T, e *Executor) {
		for _, m := range []string{"send", "react", "draft.approve"} {
			reqCtx, hangUp := context.WithCancel(t.Context())
			f := newFake(20 * time.Second)
			f.honourCtx = true
			go func() { <-time.After(5 * time.Second); hangUp() }()
			if _, err := e.Run(reqCtx, "op", m, false, f.run); !errors.Is(err, context.Canceled) {
				t.Fatalf("%s: %v, want the hang-up", m, err)
			}
			<-time.After(20 * time.Second)
			synctest.Wait()
			e.mu.Lock()
			var x *execution
			for _, c := range e.execs {
				x = c
			}
			e.mu.Unlock()
			if x == nil {
				t.Fatalf("%s: the detached send is not pollable", m)
			}
			if v := wantState(t, e, "op", x.id, StateDone); !strings.Contains(string(v.Result), `"n":1`) {
				t.Errorf("%s: result %s", m, v.Result)
			}
			if f.calls.Load() != 1 {
				t.Errorf("%s ran %d times", m, f.calls.Load())
			}
		}
		reqCtx, hangUp := context.WithCancel(t.Context())
		g := newFake(20 * time.Second)
		g.honourCtx = true
		go func() { <-time.After(5 * time.Second); hangUp() }()
		if _, err := e.Run(reqCtx, "op", "history", false, g.run); !errors.Is(err, context.Canceled) {
			t.Fatalf("history: %v", err)
		}
		synctest.Wait()
		if n, _, _ := e.counts(); n != 0 {
			t.Errorf("a non-send method outlived its request: %d in flight", n)
		}
	})
}

// WA-24B-REVIEW-FIXES L3: a send whose client hung up leaves a result nobody
// could fetch; evicting it is not a lost result, so it neither counts in
// EvictedUnfetchedTotal nor outranks a result a caller can still fetch.
func TestSendClass_HungUpResultsAreNotCountedLost(t *testing.T) {
	executorTest(t, ExecLimits{InFlight: 1000, InFlightPer: 1000, Jobs: 1000, JobsPer: 1000, Retention: time.Hour}, func(t *testing.T, e *Executor) {
		for i := range 2 * ownerRetained {
			reqCtx, hangUp := context.WithCancel(t.Context())
			f := newFake(2 * time.Second)
			go func() { <-time.After(time.Second); hangUp() }()
			if _, err := e.Run(reqCtx, "op", "send", false, f.run); !errors.Is(err, context.Canceled) {
				t.Fatalf("send %d: %v", i, err)
			}
			<-time.After(3 * time.Second)
			synctest.Wait()
		}
		if n := e.lost.Load(); n != 0 {
			t.Fatalf("%d hung-up results counted as lost unfetched, want 0", n)
		}
		// Control: finished async jobs nobody polled are lost when evicted.
		for range 2 * ownerRetained {
			if _, err := e.Run(t.Context(), "op2", "media.download", true, newFake(time.Second).run); err != nil {
				t.Fatal(err)
			}
			<-time.After(2 * time.Second)
			synctest.Wait()
		}
		if e.lost.Load() == 0 {
			t.Fatal("control: unpolled async results evicted but none counted lost")
		}
	})
}

// TestSendClass_CancelAfterTheFrameReportsTheOutcome (F4): jobs.cancel on a
// running send cannot stop its detached wire call, so the job must report
// what the call returned (done, or failed with ErrSendUncertain), never
// "cancelled" for a message that may be out. Another method stays cancelled.
func TestSendClass_CancelAfterTheFrameReportsTheOutcome(t *testing.T) {
	executorTest(t, roomy, func(t *testing.T, e *Executor) {
		for _, c := range []struct {
			method, state string
			err           error
		}{
			{"send", StateDone, nil},
			{"send", StateFailed, domain.NewSendUncertain("WAID", errors.New("ack timeout"))},
			{"send", StateCancelled, errors.New("plain")}, // a plain error after a cancel is the cancel
			{"sync.force", StateCancelled, nil},
		} {
			f := newFake(60 * time.Second) // outlives the 45 s return deadline; ignores ctx
			f.err = c.err
			r := decode(t, mustRun(t, e, c.method, f), nil)
			if _, err := e.Cancel(t.Context(), own("op"), r.JobID); err != nil {
				t.Fatalf("%s cancel: %v", c.method, err)
			}
			<-time.After(70 * time.Second)
			synctest.Wait()
			v := wantStateAny(t, e, r.JobID)
			if v.State != c.state {
				t.Errorf("%s err=%v: state %s, want %s", c.method, c.err, v.State, c.state)
			}
			if errors.Is(c.err, domain.ErrSendUncertain) && !errors.Is(v.Err, domain.ErrSendUncertain) {
				t.Errorf("uncertain outcome lost: %v", v.Err)
			}
		}
	})
}

func mustRun(t *testing.T, e *Executor, method string, f *fake) json.RawMessage {
	t.Helper()
	raw, err := e.Run(t.Context(), "op", method, method == "sync.force", f.run)
	if err != nil {
		t.Fatalf("%s: %v", method, err)
	}
	return raw
}

func wantStateAny(t *testing.T, e *Executor, id string) JobView {
	t.Helper()
	v, err := e.Get(t.Context(), own("op"), id)
	if err != nil {
		t.Fatalf("get %s: %v", id, err)
	}
	return v
}
