package app

import (
	"fmt"
	"sync"
	"time"

	"golang.org/x/time/rate"

	"github.com/yolo-labz/wa/internal/domain"
)

// Default rate limiter parameters per contracts/rate-limiter.md.
const (
	defaultPerSecondRate  = 2.0
	defaultPerSecondBurst = 2
	defaultPerMinuteRate  = 30.0 / 60.0 // 0.5 tokens/s
	defaultPerMinuteBurst = 30
	defaultPerDayRate     = 1000.0 / 86400.0 // ~0.012 tokens/s
	defaultPerDayBurst    = 1000

	// Feature 009 — FR-031, FR-032: per-recipient and new-recipient caps.
	defaultPerRecipientDaily = 30
	defaultNewRecipientDaily = 15
)

// KnownRecipientFunc reports whether the user has ever sent a message
// to the given JID. Used by the new-recipient daily cap. Injected by
// the composition root to avoid importing sqlitehistory in the app layer.
// Feature 009 — FR-032.
type KnownRecipientFunc func(jid domain.JID) bool

// RateLimiter enforces per-second, per-minute, and per-day token bucket
// limits with an optional warmup multiplier. Feature 009 added per-recipient
// daily caps (FR-031) and unique-new-recipient daily caps (FR-032).
// It is safe for concurrent use.
type RateLimiter struct {
	perSecond      *rate.Limiter
	perMinute      *rate.Limiter
	perDay         *rate.Limiter
	warmup         float64
	sessionCreated time.Time

	// Feature 009 — per-recipient + new-recipient tracking.
	mu              sync.Mutex
	recipientDaily  map[domain.JID]int // per-JID daily count, reset daily
	newRecipientCnt int                // unique new recipients today
	dayStart        time.Time          // start of current tracking day
	knownRecipient  KnownRecipientFunc // nil = disable new-recipient check
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
		recipientDaily: make(map[domain.JID]int),
		dayStart:       time.Now().Truncate(24 * time.Hour),
	}
}

// SetKnownRecipientFunc sets the callback for new-recipient detection.
// Feature 009 — FR-032.
func (r *RateLimiter) SetKnownRecipientFunc(fn KnownRecipientFunc) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.knownRecipient = fn
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

// AllowFor checks the global rate limits (via Allow) AND per-recipient
// daily caps. Returns nil if all checks pass. Feature 009 — FR-031, FR-032.
func (r *RateLimiter) AllowFor(jid domain.JID) error {
	if err := r.Allow(); err != nil {
		return err
	}
	return r.checkRecipientLimits(jid)
}

func (r *RateLimiter) checkRecipientLimits(jid domain.JID) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	// Reset daily counters if we crossed midnight.
	now := time.Now()
	today := now.Truncate(24 * time.Hour)
	if today.After(r.dayStart) {
		r.recipientDaily = make(map[domain.JID]int)
		r.newRecipientCnt = 0
		r.dayStart = today
	}

	// FR-031: per-recipient daily cap.
	count := r.recipientDaily[jid]
	if count >= defaultPerRecipientDaily {
		reset := r.dayStart.Add(24 * time.Hour)
		return fmt.Errorf("%w: %s hit %d/%d daily cap, resets at %s",
			ErrRateLimited, jid, count, defaultPerRecipientDaily,
			reset.Format("15:04"))
	}

	// FR-032: unique-new-recipient daily cap.
	if r.knownRecipient != nil && count == 0 && !r.knownRecipient(jid) {
		if r.newRecipientCnt >= defaultNewRecipientDaily {
			return fmt.Errorf("%w: %d/%d new recipients today",
				ErrRateLimited, r.newRecipientCnt, defaultNewRecipientDaily)
		}
		r.newRecipientCnt++
	}

	r.recipientDaily[jid] = count + 1
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
