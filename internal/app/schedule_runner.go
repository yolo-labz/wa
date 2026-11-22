package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/yolo-labz/wa/v2/internal/domain"
)

// FireFunc is invoked by ScheduleRunner when a timer fires. Implementations
// MUST re-load the row from ScheduledStore before acting (state may have
// flipped to cancelled via a concurrent schedule.cancel call) and must be
// safe under concurrent invocation from the timer goroutines.
type FireFunc func(ctx context.Context, profile, id string)

// ScheduleRunner owns the in-process time.Timer set backing FR-111. Start
// loads every pending row for Profile from ScheduledStore and arms a
// Timer per row; Schedule/Cancel keep the timer set consistent with
// subsequent mutations from the socket layer.
//
// Accuracy target is ±5 s (SC-08). time.AfterFunc is used so the runner
// composes with testing/synctest for deterministic tests.
type ScheduleRunner struct {
	Store   ScheduledStore
	Profile string
	Logger  *slog.Logger
	Fire    FireFunc
	Now     func() time.Time

	mu     sync.Mutex
	ctx    context.Context //nolint:containedctx // captured at Start for timer callbacks (daemon lifetime)
	cancel context.CancelFunc
	timers map[string]*time.Timer
	// stopped flips in Stop and gates new fires: a timer callback that
	// wins the race with Stop checks it under mu before registering in
	// fireWg, so Stop's Wait observes every in-flight Fire (CON-05).
	stopped bool
	fireWg  sync.WaitGroup
}

// NewScheduleRunner constructs a runner with Now=time.Now and the default
// slog logger. Store/Fire MUST be non-nil.
func NewScheduleRunner(store ScheduledStore, profile string, fire FireFunc) *ScheduleRunner {
	return &ScheduleRunner{
		Store:   store,
		Profile: profile,
		Fire:    fire,
		Logger:  slog.Default(),
		Now:     time.Now,
		timers:  make(map[string]*time.Timer),
	}
}

func runnerKey(profile, id string) string { return profile + "|" + id }

// Start loads every pending row for the runner's profile and arms a
// time.Timer per row. Past-due rows arm with a zero delay so the runner
// drains them on the next scheduler tick. The ctx is retained for timer
// callbacks — callers MUST pass the daemon lifetime ctx, never a request
// ctx.
func (r *ScheduleRunner) Start(ctx context.Context) error {
	if r.Store == nil {
		return errors.New("schedule runner: nil store")
	}
	if r.Fire == nil {
		return errors.New("schedule runner: nil fire func")
	}
	// Derive a runner-owned cancellable ctx so Stop can abort in-flight
	// Fire callbacks even on teardown paths that never cancel the
	// caller's ctx (CON-05).
	runCtx, cancel := context.WithCancel(ctx)
	r.mu.Lock()
	r.ctx = runCtx
	r.cancel = cancel
	r.mu.Unlock()

	r.resolveInterrupted(ctx)
	pending, err := r.Store.ListPending(ctx, r.Profile)
	if err != nil {
		return fmt.Errorf("schedule runner: list pending: %w", err)
	}
	for _, ss := range pending {
		r.arm(ss)
	}
	r.Logger.InfoContext(ctx, "schedule runner loaded",
		slog.String("profile", r.Profile),
		slog.Int("pending", len(pending)))
	return nil
}

// interruptedScanLimit bounds the rows resolved at one start; schedule.list
// shows any beyond it as sending.
const interruptedScanLimit = 1000

// resolveInterrupted settles rows a previous run left in sending. Whether the
// send reached WhatsApp is unknowable, so they are NOT re-sent: each becomes
// send_uncertain (terminal), is logged by id, and is listed by
// schedule.list --state send_uncertain for the operator to decide.
func (r *ScheduleRunner) resolveInterrupted(ctx context.Context) {
	rows, err := r.Store.List(ctx, r.Profile, domain.ScheduleSending, interruptedScanLimit)
	if err != nil {
		r.Logger.ErrorContext(ctx, "schedule runner: list sending rows", slog.String("err", err.Error()))
		return
	}
	n := 0
	for _, ss := range rows {
		u, err := ss.MarkSendUncertain(r.Now(), "daemon stopped while sending; delivery unknown, not retried")
		if err == nil {
			_, err = r.Store.UpdateFrom(ctx, domain.ScheduleSending, u)
		}
		if err != nil {
			r.Logger.ErrorContext(ctx, "schedule runner: mark send_uncertain failed",
				slog.String("id", ss.ID()), slog.String("err", err.Error()))
			continue
		}
		n++
		r.Logger.WarnContext(ctx, "schedule send uncertain: not re-sent, operator decision needed",
			slog.String("profile", r.Profile), slog.String("id", ss.ID()), slog.Time("claimed_at", ss.UpdatedAt()))
	}
	if n > 0 {
		r.Logger.WarnContext(ctx, "schedule_send_uncertain_total", slog.Int("count", n))
	}
}

// Schedule arms or re-arms a timer for ss. Terminal-state rows are a
// no-op. Safe from any goroutine once Start has returned.
func (r *ScheduleRunner) Schedule(ss domain.ScheduledSend) {
	r.arm(ss)
}

// Cancel stops and drops the timer for (profile, id). No-op if no timer
// is armed.
func (r *ScheduleRunner) Cancel(profile, id string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	key := runnerKey(profile, id)
	if t, ok := r.timers[key]; ok {
		t.Stop()
		delete(r.timers, key)
	}
}

// Pending reports the number of currently-armed timers. Intended for
// tests and status endpoints.
func (r *ScheduleRunner) Pending() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.timers)
}

// Stop cancels every armed timer, cancels the runner ctx, and waits for
// in-flight Fire callbacks to return. Callers use this at daemon
// shutdown before closing the store — the join is the happens-before
// that keeps a timer that fired just before Stop from touching a
// closed schedule store (CON-05). The ctx cancellation makes that wait
// short: Fire implementations unwind on ctx.Err().
func (r *ScheduleRunner) Stop() {
	r.mu.Lock()
	r.stopped = true
	if r.cancel != nil {
		r.cancel()
	}
	for k, t := range r.timers {
		t.Stop()
		delete(r.timers, k)
	}
	r.mu.Unlock()
	r.fireWg.Wait()
}

func (r *ScheduleRunner) arm(ss domain.ScheduledSend) {
	if ss.State() != domain.SchedulePending {
		return
	}
	now := r.Now()
	delay := max(ss.FireAt().Sub(now), 0)
	key := runnerKey(ss.Profile(), ss.ID())
	profile := ss.Profile()
	id := ss.ID()

	r.mu.Lock()
	defer r.mu.Unlock()
	if r.stopped {
		return
	}
	if r.ctx == nil {
		r.ctx = context.Background()
	}
	fireCtx := r.ctx
	if old, ok := r.timers[key]; ok {
		old.Stop()
	}
	r.timers[key] = time.AfterFunc(delay, func() {
		// stopped + fireWg.Add under the same mu Stop takes: either
		// this callback registers before Stop's Wait (which then
		// blocks until Fire returns), or it observes stopped and
		// never fires (CON-05).
		r.mu.Lock()
		if r.stopped {
			r.mu.Unlock()
			return
		}
		r.fireWg.Add(1)
		delete(r.timers, key)
		r.mu.Unlock()
		defer r.fireWg.Done()
		r.Fire(fireCtx, profile, id)
	})
}
