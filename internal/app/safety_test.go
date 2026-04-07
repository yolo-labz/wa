package app

import (
	"errors"
	"testing"
	"time"

	"github.com/yolo-labz/wa/internal/domain"
)

// stubAllowlist is a test double that either allows or denies everything.
type stubAllowlist struct {
	allow bool
}

func (s stubAllowlist) Allows(_ domain.JID, _ domain.Action) bool {
	return s.allow
}

func testJID(t *testing.T) domain.JID {
	t.Helper()
	j, err := domain.Parse("5511999999999@s.whatsapp.net")
	if err != nil {
		t.Fatal(err)
	}
	return j
}

// TestSafety_AllowlistDeny verifies that when the allowlist denies, the
// rate limiter is not consulted (no token consumed).
func TestSafety_AllowlistDeny(t *testing.T) {
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	rl := NewRateLimiterAt(now.Add(-30*24*time.Hour), now) // warmup 1.0
	sp := NewSafetyPipeline(stubAllowlist{allow: false}, rl)

	jid := testJID(t)
	err := sp.Check(jid, domain.ActionSend)
	if !errors.Is(err, ErrNotAllowlisted) {
		t.Fatalf("expected ErrNotAllowlisted, got %v", err)
	}

	// Rate limiter should still have full burst (no token consumed).
	// If the pipeline incorrectly checked rate limiter first, one token
	// would be consumed.
	for i := 0; i < 2; i++ {
		if err := rl.Allow(); err != nil {
			t.Fatalf("rl.Allow() #%d: expected success (no token consumed by deny), got %v", i+1, err)
		}
	}
}

// TestSafety_RateLimitDeny verifies that when the allowlist allows but the
// rate limiter is exhausted, ErrRateLimited is returned.
func TestSafety_RateLimitDeny(t *testing.T) {
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	rl := NewRateLimiterAt(now.Add(-30*24*time.Hour), now) // warmup 1.0
	sp := NewSafetyPipeline(stubAllowlist{allow: true}, rl)

	jid := testJID(t)

	// Exhaust per-second burst.
	for i := 0; i < 2; i++ {
		if err := sp.Check(jid, domain.ActionSend); err != nil {
			t.Fatalf("Check() #%d: unexpected error: %v", i+1, err)
		}
	}
	err := sp.Check(jid, domain.ActionSend)
	if !errors.Is(err, ErrRateLimited) {
		t.Fatalf("expected ErrRateLimited, got %v", err)
	}
}

// TestSafety_WarmupDeny verifies that during warmup, exhaustion returns
// ErrWarmupActive.
func TestSafety_WarmupDeny(t *testing.T) {
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	rl := NewRateLimiterAt(now, now) // warmup 0.25, burst 1
	sp := NewSafetyPipeline(stubAllowlist{allow: true}, rl)

	jid := testJID(t)

	// First request should pass (burst 1).
	if err := sp.Check(jid, domain.ActionSend); err != nil {
		t.Fatalf("Check() #1: unexpected error: %v", err)
	}
	// Second should fail with warmup error.
	err := sp.Check(jid, domain.ActionSend)
	if !errors.Is(err, ErrWarmupActive) {
		t.Fatalf("expected ErrWarmupActive, got %v", err)
	}
}

// TestSafety_AllowlistAllowAndUnderRate verifies the happy path.
func TestSafety_AllowlistAllowAndUnderRate(t *testing.T) {
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	rl := NewRateLimiterAt(now.Add(-30*24*time.Hour), now) // warmup 1.0
	sp := NewSafetyPipeline(stubAllowlist{allow: true}, rl)

	jid := testJID(t)
	if err := sp.Check(jid, domain.ActionSend); err != nil {
		t.Fatalf("Check() happy path: unexpected error: %v", err)
	}
}
