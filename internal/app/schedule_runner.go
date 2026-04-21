package app

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/yolo-labz/wa/internal/domain"
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
	timers map[string]*time.Timer
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
		return fmt.Errorf("schedule runner: nil store")
	}
	if r.Fire == nil {
		return fmt.Errorf("schedule runner: nil fire func")
	}
	r.mu.Lock()
	r.ctx = ctx
	r.mu.Unlock()

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

// Stop cancels every armed timer. Callers use this at daemon shutdown
// before closing the store.
func (r *ScheduleRunner) Stop() {
	r.mu.Lock()
	defer r.mu.Unlock()
	for k, t := range r.timers {
		t.Stop()
		delete(r.timers, k)
	}
}

func (r *ScheduleRunner) arm(ss domain.ScheduledSend) {
	if ss.State() != domain.SchedulePending {
		return
	}
	now := r.Now()
	delay := ss.FireAt().Sub(now)
	if delay < 0 {
		delay = 0
	}
	key := runnerKey(ss.Profile(), ss.ID())
	profile := ss.Profile()
	id := ss.ID()

	r.mu.Lock()
	defer r.mu.Unlock()
	if r.ctx == nil {
		r.ctx = context.Background()
	}
	fireCtx := r.ctx
	if old, ok := r.timers[key]; ok {
		old.Stop()
	}
	r.timers[key] = time.AfterFunc(delay, func() {
		r.mu.Lock()
		delete(r.timers, key)
		r.mu.Unlock()
		r.Fire(fireCtx, profile, id)
	})
}
