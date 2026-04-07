package app

import "github.com/yolo-labz/wa/internal/domain"

// SafetyPipeline composes the allowlist check and rate limiter into a
// single Check call. Pipeline order per contracts/dispatcher-impl.md:
// allowlist → rate limiter. If allowlist denies, rate limiter is not
// consulted (no token consumed).
type SafetyPipeline struct {
	allowlist Allowlist
	limiter   *RateLimiter
}

// NewSafetyPipeline constructs a safety pipeline from the given
// allowlist port and rate limiter.
func NewSafetyPipeline(al Allowlist, rl *RateLimiter) *SafetyPipeline {
	return &SafetyPipeline{allowlist: al, limiter: rl}
}

// Check runs the safety pipeline for an outbound action to jid.
// Returns nil on success, ErrNotAllowlisted, ErrRateLimited, or
// ErrWarmupActive on denial.
func (s *SafetyPipeline) Check(jid domain.JID, action domain.Action) error {
	if !s.allowlist.Allows(jid, action) {
		return ErrNotAllowlisted
	}
	return s.limiter.Allow()
}
