package app

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"
)

// Execution states as jobs.get reports them (FR-116-16).
const (
	StateCapacityWaiting = "capacity_waiting"
	StateQueued          = "queued"
	StateRunning         = "running"
	StateCancelRequested = "cancellation_requested"
	StateDone            = "done"
	StateFailed          = "failed"
	StateCancelled       = "cancelled"
	StateExpired         = "expired"
)

// Executor errors; S4c maps them to typed RPC errors.
var (
	ErrExecCapacity       = errors.New("execution capacity exceeded")
	ErrJobNotFound        = errors.New("job not found")
	ErrUnclassifiedMethod = errors.New("method has no deadline class")
	ErrAsyncNotJob        = errors.New("async is only for job-capable methods")
)

// ExecLimits bounds the two admission pools and result retention.
type ExecLimits struct {
	InFlight, InFlightPer int // ordinary synchronous executions
	Jobs, JobsPer         int // async and deadline-promoted executions
	Retention             time.Duration
}

// DefaultExecLimits is FR-116-16: 64/16 in flight, 16/2 jobs, 1 h results.
var DefaultExecLimits = ExecLimits{InFlight: 64, InFlightPer: 16, Jobs: 16, JobsPer: 2, Retention: time.Hour}

// reapEvery: ponytail: a fixed tick, so expiry is at most this late.
const reapEvery = time.Minute

type pool int

const (
	poolInFlight pool = iota
	poolJob
)

type execution struct {
	id, owner          string
	pool               pool
	state              string
	cancel             context.CancelFunc
	promoted, released bool // returned as a job / slot given back
	exited, tomb       bool // worker returned / no slot, kept only to answer polls
	result             json.RawMessage
	err                error
	finished, tombAt   time.Time
	done               chan struct{}
}

// JobView is one jobs.get answer; Result and Err only once terminal.
type JobView struct {
	ID       string          `json:"job_id"`
	State    string          `json:"state"`
	TimedOut bool            `json:"timed_out,omitempty"`
	Result   json.RawMessage `json:"result,omitempty"`
	Err      error           `json:"-"`
}

// Executor runs every dispatch under its method's deadline class
// (FR-116-16): one random id before start, never a wait past
// ReturnDeadline, never a second run. Workers derive from the executor,
// not the request, and are signalled but never joined; a slot stays
// charged until its worker exits and, for a job, until its result is
// fetched or expires. Then it lingers slot-free in a bounded FIFO.
type Executor struct {
	limits ExecLimits
	ctx    context.Context
	stop   context.CancelFunc
	reaped chan struct{}

	mu      sync.Mutex
	execs   map[string]*execution
	count   [2]int
	per     [2]map[string]int
	waiting []*execution // capacity_waiting, oldest first
	tombs   []string
}

// NewExecutor starts an executor and its reaper; Close stops both.
func NewExecutor(limits ExecLimits) *Executor {
	ctx, stop := context.WithCancel(context.Background())
	e := &Executor{
		limits: limits, ctx: ctx, stop: stop, reaped: make(chan struct{}),
		execs: map[string]*execution{}, per: [2]map[string]int{{}, {}},
	}
	go e.reap()
	return e
}

// Close cancels every worker, refuses admission and stops the reaper,
// without waiting for workers.
func (e *Executor) Close() {
	e.stop()
	<-e.reaped
}

// Run executes fn once for owner, refusing admission before fn runs.
// async (job methods only) answers {job_id, state:"queued"} at once.
// Otherwise a result before ReturnDeadline keeps its synchronous shape;
// at the deadline the same execution is promoted and Run answers
// {job_id, state, timed_out:true} without joining it.
func (e *Executor) Run(ctx context.Context, owner, method string, async bool, fn func(context.Context) (json.RawMessage, error)) (json.RawMessage, error) {
	dl, ok := MethodDeadlines[method]
	if !ok {
		return nil, fmt.Errorf("%w: %s", ErrUnclassifiedMethod, method)
	}
	if async && dl.Class != Job {
		return nil, fmt.Errorf("%w: %s", ErrAsyncNotJob, method)
	}
	x, err := e.admit(owner, async, dl.MaxWorker, fn)
	if err != nil || async {
		return jobReply(x, StateQueued, false, err)
	}
	stopReq := context.AfterFunc(ctx, func() { e.cancelExec(x) })
	timer := time.NewTimer(ReturnDeadline)
	defer timer.Stop()
	select {
	case <-x.done:
		stopReq()
		return e.collect(x)
	case <-timer.C:
		if !stopReq() {
			return nil, ctx.Err()
		}
		if state, finished := e.promote(x); !finished {
			return jobReply(x, state, true, nil)
		}
		return e.collect(x)
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (e *Executor) admit(owner string, async bool, life time.Duration, fn func(context.Context) (json.RawMessage, error)) (*execution, error) {
	var idb [16]byte
	_, _ = rand.Read(idb[:]) // crypto/rand.Read never fails (Go 1.24+)
	x := &execution{id: hex.EncodeToString(idb[:]), owner: owner, state: StateRunning, done: make(chan struct{})}
	if async {
		x.pool, x.state, x.promoted = poolJob, StateQueued, true
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.ctx.Err() != nil || !e.roomLocked(x.pool, owner) {
		return nil, ErrExecCapacity
	}
	e.chargeLocked(x, x.pool, 1)
	e.execs[x.id] = x
	wctx, cancel := context.WithTimeout(e.ctx, life)
	x.cancel = cancel
	go e.work(wctx, x, fn)
	return x, nil
}

func (e *Executor) work(ctx context.Context, x *execution, fn func(context.Context) (json.RawMessage, error)) {
	e.mu.Lock()
	if x.state == StateQueued {
		x.state = StateRunning
	}
	e.mu.Unlock()
	res, err := runRecovered(ctx, fn)
	x.cancel()
	e.mu.Lock()
	defer e.mu.Unlock()
	x.exited, x.finished = true, time.Now()
	switch {
	case x.state == StateCancelRequested || e.ctx.Err() != nil: // a late result is never delivered
		x.state = StateCancelled
		e.retireLocked(x)
	case err != nil:
		x.state, x.err = StateFailed, err
	default:
		x.state, x.result = StateDone, res
	}
	close(x.done)
}

func runRecovered(ctx context.Context, fn func(context.Context) (json.RawMessage, error)) (res json.RawMessage, err error) {
	defer func() {
		if r := recover(); r != nil {
			res, err = nil, fmt.Errorf("execution panicked: %v", r)
		}
	}()
	return fn(ctx)
}

// collect hands a synchronous caller its result and forgets the execution.
func (e *Executor) collect(x *execution) (json.RawMessage, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.releaseLocked(x)
	delete(e.execs, x.id)
	if x.state == StateCancelled {
		return nil, context.Canceled
	}
	return x.result, x.err
}

// promote makes the execution a job: in the job pool if it has room,
// else capacity_waiting, still charged in flight.
func (e *Executor) promote(x *execution) (string, bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if x.exited {
		return x.state, true
	}
	x.promoted = true
	if e.roomLocked(poolJob, x.owner) {
		e.moveLocked(x)
	} else {
		x.state = StateCapacityWaiting
		e.waiting = append(e.waiting, x)
	}
	return x.state, false
}

// Get reports owner's execution id; fetching a terminal result frees its
// slot, and the outcome stays pollable until retention ends.
func (e *Executor) Get(owner, id string) (JobView, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	x, ok := e.execs[id]
	if !ok || x.owner != owner || !x.promoted {
		return JobView{}, ErrJobNotFound
	}
	v := JobView{ID: id, State: x.state}
	if x.state == StateDone || x.state == StateFailed {
		v.Result, v.Err = x.result, x.err
		e.retireLocked(x)
	}
	return v, nil
}

// Cancel signals owner's execution id without joining its worker; the
// slot stays charged until the worker exits.
func (e *Executor) Cancel(owner, id string) (JobView, error) {
	e.mu.Lock()
	if x, ok := e.execs[id]; ok && x.owner == owner && x.promoted {
		e.cancelLocked(x)
	}
	e.mu.Unlock()
	return e.Get(owner, id)
}

func (e *Executor) cancelExec(x *execution) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.cancelLocked(x)
}

func (e *Executor) cancelLocked(x *execution) {
	if !x.exited {
		x.state = StateCancelRequested
		x.cancel()
	}
}

func (e *Executor) roomLocked(p pool, owner string) bool {
	maxAll, maxPer := e.limits.InFlight, e.limits.InFlightPer
	if p == poolJob {
		maxAll, maxPer = e.limits.Jobs, e.limits.JobsPer
	}
	return e.count[p] < maxAll && e.per[p][owner] < maxPer
}

func (e *Executor) chargeLocked(x *execution, p pool, delta int) {
	e.count[p] += delta
	if e.per[p][x.owner] += delta; e.per[p][x.owner] == 0 {
		delete(e.per[p], x.owner)
	}
}

// moveLocked transfers the same execution to the job pool.
func (e *Executor) moveLocked(x *execution) {
	e.chargeLocked(x, poolInFlight, -1)
	e.chargeLocked(x, poolJob, 1)
	x.pool = poolJob
	if x.state == StateCapacityWaiting {
		x.state = StateRunning
	}
}

func (e *Executor) releaseLocked(x *execution) {
	if x.released {
		return
	}
	x.released = true
	e.chargeLocked(x, x.pool, -1)
	// Freed job quota goes to the oldest still-running waiter; a waiter
	// that already finished keeps its in-flight entry until fetched.
	kept := e.waiting[:0]
	for _, w := range e.waiting {
		switch {
		case w.state != StateCapacityWaiting || w.exited || w.released:
		case e.roomLocked(poolJob, w.owner):
			e.moveLocked(w)
		default:
			kept = append(kept, w)
		}
	}
	e.waiting = kept
}

// retireLocked frees x's slot and keeps it pollable, evicting the
// oldest such entry beyond the pools' combined size.
func (e *Executor) retireLocked(x *execution) {
	e.releaseLocked(x)
	if x.tomb {
		return
	}
	x.tomb, x.tombAt = true, time.Now()
	e.tombs = append(e.tombs, x.id)
	if len(e.tombs) > e.limits.InFlight+e.limits.Jobs {
		if old, ok := e.execs[e.tombs[0]]; ok && old.tomb {
			delete(e.execs, old.id)
		}
		e.tombs = e.tombs[1:]
	}
}

func (e *Executor) reap() {
	defer close(e.reaped)
	t := time.NewTicker(reapEvery)
	defer t.Stop()
	for {
		select {
		case <-e.ctx.Done():
			return
		case now := <-t.C:
			e.expire(now)
		}
	}
}

// expire drops results older than retention and forgets old tombstones.
func (e *Executor) expire(now time.Time) {
	e.mu.Lock()
	defer e.mu.Unlock()
	for id, x := range e.execs {
		switch {
		case x.promoted && (x.state == StateDone || x.state == StateFailed) && now.Sub(x.finished) >= e.limits.Retention:
			x.state, x.result, x.err = StateExpired, nil, nil
			e.retireLocked(x)
		case x.tomb && now.Sub(x.tombAt) >= e.limits.Retention:
			delete(e.execs, id)
		}
	}
}

func jobReply(x *execution, state string, timedOut bool, err error) (json.RawMessage, error) {
	if err != nil {
		return nil, err
	}
	return json.Marshal(JobView{ID: x.id, State: state, TimedOut: timedOut})
}
