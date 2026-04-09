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
	defaultPerDayRate     = 1000.0 / 86400.0 // ~0.012 tokens/s
	defaultPerDayBurst    = 1000
)

// RateLimiter enforces per-second, per-minute, and per-day token bucket
// limits with an optional warmup multiplier. It is safe for concurrent
// use: all three *rate.Limiter instances are documented as goroutine-safe.
type RateLimiter struct {
	perSecond      *rate.Limiter
	perMinute      *rate.Limiter
	perDay         *rate.Limiter
	warmup         float64
	sessionCreated time.Time
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
	b := int(float64(defaultBurst) * multiplier)
	if b < 1 {
		b = 1
	}
	return b
}

// NewRateLimiter creates a rate limiter with the warmup multiplier
// computed from the session creation time. The multiplier is fixed at
// construction and does not change during the limiter's lifetime
// (contracts/rate-limiter.md §Recalculation).
func NewRateLimiter(sessionCreated time.Time) *RateLimiter {
	m := warmupMultiplier(sessionCreated, time.Now())
	return newRateLimiterWithMultiplier(sessionCreated, m)
}

// NewRateLimiterAt creates a rate limiter using the given "now" time for
// warmup computation. Exists for deterministic testing.
func NewRateLimiterAt(sessionCreated, now time.Time) *RateLimiter {
	m := warmupMultiplier(sessionCreated, now)
	return newRateLimiterWithMultiplier(sessionCreated, m)
}

func newRateLimiterWithMultiplier(sessionCreated time.Time, m float64) *RateLimiter {
	return &RateLimiter{
		perSecond:      rate.NewLimiter(rate.Limit(defaultPerSecondRate*m), scaledBurst(defaultPerSecondBurst, m)),
		perMinute:      rate.NewLimiter(rate.Limit(defaultPerMinuteRate*m), scaledBurst(defaultPerMinuteBurst, m)),
		perDay:         rate.NewLimiter(rate.Limit(defaultPerDayRate*m), scaledBurst(defaultPerDayBurst, m)),
		warmup:         m,
		sessionCreated: sessionCreated,
	}
}

// Allow checks all three buckets in order: per-second, per-minute,
// per-day. If any bucket is exhausted, the request is rejected. Returns
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
	if !r.perDay.Allow() {
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
