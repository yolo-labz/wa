package app

import (
	"context"
	"log/slog"
	"time"
)

// IdempotencySweeper is the FR-034a background goroutine that evicts
// expired idempotency rows every 5 minutes. The interval is hard-coded
// because the TTL is fixed at 24h — there is no reason for an operator
// to tune this knob and exposing it adds a configuration surface that
// can drift from the contract.
const idempotencySweepInterval = 5 * time.Minute

// SweepableStore narrows IdempotencyStore to the single method the
// sweeper needs; lets tests pass in minimal fakes without wiring
// LoadOrStore or the legacy Check/Record surface.
type SweepableStore interface {
	Sweep(ctx context.Context, before time.Time) (int, error)
}

// RunIdempotencySweeper loops until ctx is done, calling store.Sweep
// every 5 minutes. Use log.Info on each eviction count so operators can
// spot runaway cardinality. Does not block on ctx shutdown.
func RunIdempotencySweeper(ctx context.Context, store SweepableStore, log *slog.Logger) {
	if log == nil {
		log = slog.Default()
	}
	ticker := time.NewTicker(idempotencySweepInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-ticker.C:
			n, err := store.Sweep(ctx, now)
			if err != nil {
				log.Warn("idempotency sweep failed", "err", err)
				continue
			}
			if n > 0 {
				log.Info("idempotency sweep evicted", "count", n)
			}
		}
	}
}
