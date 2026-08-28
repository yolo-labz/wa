package app

import (
	"time"

	"golang.org/x/time/rate"
)

// Default rate limiter parameters per contracts/rate-limiter.md.
const (
	defaultPerSecondRate  = 2.0
	defaultPerSecondBurst = 2
	defaultPerMinuteRate  = 30.0 / 60.0 // 0.5 tokens/s
	defaultPerMinuteBurst = 30
)

// RateLimiter enforces the non-overridable short-window token buckets with
// an optional warmup multiplier. Ordinary sends deliberately have no daily
// budget; group-administration daily caps live in its separate adapter.
type RateLimiter struct {
	perSecond *rate.Limiter
	perMinute *rate.Limiter
	warmup    float64
}

// warmupMultiplier returns the warmup scaling factor for the given
// session age. Pure function — no side effects.
func warmupMultiplier(created, now time.Time) float64 {
	age := now.Sub(created)
	switch {
	case age < 7*24*time.Hour:
		return 0.25
	case age < 14*24*time.Hour:
		return 0.50
	default:
		return 1.0
	}
}

// scaledBurst computes max(1, int(defaultBurst * multiplier)) so burst
// is never zero — a zero burst would make the limiter permanently deny.
func scaledBurst(defaultBurst int, multiplier float64) int {
	b := max(int(float64(defaultBurst)*multiplier), 1)
	return b
}

// NewRateLimiter creates a rate limiter with the warmup multiplier
// computed from the session creation time. The multiplier is fixed at
// construction and does not change during the limiter's lifetime
// (contracts/rate-limiter.md §Recalculation).
func NewRateLimiter(sessionCreated time.Time) *RateLimiter {
	m := warmupMultiplier(sessionCreated, time.Now())
	return newRateLimiterWithMultiplier(m)
}

// NewRateLimiterAt creates a rate limiter using the given "now" time for
// warmup computation. Exists for deterministic testing.
func NewRateLimiterAt(sessionCreated, now time.Time) *RateLimiter {
	m := warmupMultiplier(sessionCreated, now)
	return newRateLimiterWithMultiplier(m)
}

func newRateLimiterWithMultiplier(m float64) *RateLimiter {
	return &RateLimiter{
		perSecond: rate.NewLimiter(rate.Limit(defaultPerSecondRate*m), scaledBurst(defaultPerSecondBurst, m)),
		perMinute: rate.NewLimiter(rate.Limit(defaultPerMinuteRate*m), scaledBurst(defaultPerMinuteBurst, m)),
		warmup:    m,
	}
}

// Allow checks both short-window buckets in order: per-second, then
// per-minute. If either bucket is exhausted, the request is rejected. Returns
// nil on success, ErrRateLimited or ErrWarmupActive on rejection.
//
// Per contracts/rate-limiter.md §Allow check: if per-second passes but
// per-minute rejects, the per-second token is "wasted" — conservative
// is the safe direction.
func (r *RateLimiter) Allow() error {
	if !r.perSecond.Allow() {
		return r.denyError()
	}
	if !r.perMinute.Allow() {
		return r.denyError()
	}
	return nil
}

// Warmup returns the current warmup multiplier (0.25, 0.50, or 1.0).
func (r *RateLimiter) Warmup() float64 { return r.warmup }

// denyError returns ErrWarmupActive when the warmup multiplier is < 1.0,
// ErrRateLimited otherwise.
func (r *RateLimiter) denyError() error {
	if r.warmup < 1.0 {
		return ErrWarmupActive
	}
	return ErrRateLimited
}
