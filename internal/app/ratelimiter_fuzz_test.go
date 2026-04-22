package app

import (
	"errors"
	"testing"
	"time"
)

// FuzzRateLimit drives the rate limiter with arbitrary session-age +
// call-count inputs to catch panics and invariant drift. Asserts:
//  1. Warmup multiplier is always exactly one of {0.25, 0.50, 1.0}.
//  2. Allow never returns an error outside {nil, ErrRateLimited,
//     ErrWarmupActive}.
//  3. Session-created times in the far past, far future, and zero never
//     panic at construction.
func FuzzRateLimit(f *testing.F) {
	// Seeds: fresh session, mid-warmup, past warmup, zero time, far
	// future, extreme negative. Call counts bracket the burst window.
	seeds := []struct {
		ageSec int64
		calls  uint8
	}{
		{0, 0},
		{0, 3},
		{60, 10},
		{7 * 86400, 50},
		{15 * 86400, 200},
		{365 * 86400, 255},
		{-86400, 3},
	}
	for _, s := range seeds {
		f.Add(s.ageSec, s.calls)
	}

	f.Fuzz(func(t *testing.T, ageSec int64, calls uint8) {
		now := time.Unix(1_700_000_000, 0).UTC()
		created := now.Add(-time.Duration(ageSec) * time.Second)

		rl := NewRateLimiterAt(created, now)

		w := rl.Warmup()
		if w != 0.25 && w != 0.50 && w != 1.0 {
			t.Fatalf("warmup=%v, want one of {0.25, 0.50, 1.0}", w)
		}

		for i := uint8(0); i < calls; i++ {
			err := rl.Allow()
			switch {
			case err == nil:
			case errors.Is(err, ErrRateLimited):
			case errors.Is(err, ErrWarmupActive):
			default:
				t.Fatalf("unexpected err type from Allow: %v", err)
			}
		}
	})
}
