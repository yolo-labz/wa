package sqlitestore

import (
	"context"
	"time"
)

// WarmupEpoch returns the instant the rate limiter should treat as the start
// of this session's life, and false when nothing has been recorded yet.
//
// This is deliberately NOT paired_at, and the difference is the whole point
// of the column. paired_at answers "when did the handshake happen", and for
// every session paired before issue #311 the honest answer is "unknown" —
// whatsmeow's schema never recorded it and there is nothing to back-fill
// from. Reporting that absence is correct for health's sessionSince, which
// must not substitute a number that reads as evidence.
//
// The rate limiter asks a different question: "is this account new enough
// to need throttling?" Answering it with paired_at's absence meant falling
// back to time.Now() on every boot, which re-pinned warmup day 0 at each
// restart and held a months-old account at 25% of its rate ladder
// indefinitely (issue #368).
//
// So warmup_epoch means "the first boot at which this daemon observed this
// pairing". Nothing is fabricated: it is exactly what it says, it converges
// after one boot, and a genuinely new session still warms up from its own
// first sighting.
func (s *Store) WarmupEpoch(ctx context.Context) (time.Time, bool, error) {
	return s.readEpoch(ctx, warmupEpochColumn)
}

// SetWarmupEpoch records the warmup epoch. A zero t clears it back to
// unknown, which is what a re-pair wants: the new session starts its own
// ladder.
func (s *Store) SetWarmupEpoch(ctx context.Context, t time.Time) error {
	return s.writeEpoch(ctx, warmupEpochColumn, t)
}
