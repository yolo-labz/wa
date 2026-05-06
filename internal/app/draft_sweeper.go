package app

import (
	"context"
	"errors"
	"log/slog"
	"time"
)

// DraftSweeperInterval is how often DraftSweeper.Run calls ExpireDue. The
// FR-043 expiry window is 30 minutes; sweeping every minute bounds the
// stale-pending visibility window to ≤ 1 min.
const DraftSweeperInterval = time.Minute

// DraftSweeper transitions pending_review drafts past their 30 min horizon
// to expired. It owns no state beyond the injected store + profile; every
// tick runs an ExpireDue and logs the count.
type DraftSweeper struct {
	Store    DraftStore
	Profile  string
	Interval time.Duration
	Logger   *slog.Logger
	Now      func() time.Time
}

// NewDraftSweeper constructs a sweeper with sane defaults: interval =
// DraftSweeperInterval, Now = time.Now, Logger = slog.Default.
func NewDraftSweeper(store DraftStore, profile string) *DraftSweeper {
	return &DraftSweeper{
		Store:    store,
		Profile:  profile,
		Interval: DraftSweeperInterval,
		Logger:   slog.Default(),
		Now:      time.Now,
	}
}

// Run blocks until ctx is cancelled. Each tick invokes Sweep once; errors
// are logged but never cause the loop to exit.
func (s *DraftSweeper) Run(ctx context.Context) error {
	interval := s.Interval
	if interval <= 0 {
		interval = DraftSweeperInterval
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			if n, err := s.Sweep(ctx); err != nil {
				s.Logger.ErrorContext(ctx, "draft sweep failed",
					slog.String("profile", s.Profile),
					slog.String("error", err.Error()))
			} else if n > 0 {
				s.Logger.InfoContext(ctx, "drafts expired",
					slog.String("profile", s.Profile),
					slog.Int("count", n))
			}
		}
	}
}

// Sweep is one pass — useful for tests (no ticker needed) and for eager
// sweeps triggered by `draft.list` to avoid returning already-expired
// rows.
func (s *DraftSweeper) Sweep(ctx context.Context) (int, error) {
	if s.Store == nil {
		return 0, errors.New("draft sweeper: nil store")
	}
	now := time.Now()
	if s.Now != nil {
		now = s.Now()
	}
	return s.Store.ExpireDue(ctx, s.Profile, now)
}
