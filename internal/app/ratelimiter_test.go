package app

import (
	"errors"
	"sync"
	"testing"
	"testing/synctest"
	"time"
)

// TestBurstExhaustion verifies that a fresh limiter allows exactly burst
// requests, then rejects the next (contract §1).
func TestBurstExhaustion(t *testing.T) {
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	// Session 30 days old → warmup 1.0 → full burst of 2 per-second.
	rl := NewRateLimiterAt(now.Add(-30*24*time.Hour), now)

	// Should allow exactly 2 (per-second burst).
	for i := 0; i < 2; i++ {
		if err := rl.Allow(); err != nil {
			t.Fatalf("Allow() #%d: unexpected error: %v", i+1, err)
		}
	}
	// Third should fail.
	if err := rl.Allow(); err == nil {
		t.Fatal("Allow() #3: expected error, got nil")
	}
}

// TestWarmupMultiplier verifies the pure warmupMultiplier function at
// various session ages (contract §6).
func TestWarmupMultiplier(t *testing.T) {
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	tests := []struct {
		name     string
		ageDays  int
		wantMult float64
	}{
		{"day_0", 0, 0.25},
		{"day_3", 3, 0.25},
		{"day_6", 6, 0.25},
		{"day_7", 7, 0.50},
		{"day_10", 10, 0.50},
		{"day_13", 13, 0.50},
		{"day_14", 14, 1.00},
		{"day_15", 15, 1.00},
		{"day_30", 30, 1.00},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			created := base
			now := base.Add(time.Duration(tt.ageDays) * 24 * time.Hour)
			got := warmupMultiplier(created, now)
			if got != tt.wantMult {
				t.Errorf("warmupMultiplier(age=%dd) = %v, want %v", tt.ageDays, got, tt.wantMult)
			}
		})
	}
}

// TestBurstNeverZero verifies that even at 25% warmup, burst is at least 1
// (contract §3).
func TestBurstNeverZero(t *testing.T) {
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	// Session created today → warmup 0.25.
	rl := NewRateLimiterAt(now, now)

	if rl.Warmup() != 0.25 {
		t.Fatalf("expected warmup 0.25, got %v", rl.Warmup())
	}
	// Per-second burst at 25% = max(1, int(2*0.25)) = max(1,0) = 1.
	// So exactly 1 should succeed.
	if err := rl.Allow(); err != nil {
		t.Fatalf("Allow() #1 at 25%%: unexpected error: %v", err)
	}
	// Second should fail (burst 1 exhausted).
	if err := rl.Allow(); err == nil {
		t.Fatal("Allow() #2 at 25%%: expected error, got nil")
	}
}

// TestWarmupErrorType verifies that the warmup multiplier < 1.0 returns
// ErrWarmupActive, not ErrRateLimited.
func TestWarmupErrorType(t *testing.T) {
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	rl := NewRateLimiterAt(now, now) // warmup 0.25

	// Exhaust burst.
	_ = rl.Allow()
	err := rl.Allow()
	if err == nil {
		t.Fatal("expected error")
	}
	if !errors.Is(err, ErrWarmupActive) {
		t.Errorf("expected ErrWarmupActive, got %v", err)
	}
}

// TestFullRateErrorType verifies that at warmup 1.0, exhaustion returns
// ErrRateLimited.
func TestFullRateErrorType(t *testing.T) {
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	rl := NewRateLimiterAt(now.Add(-30*24*time.Hour), now) // warmup 1.0

	_ = rl.Allow()
	_ = rl.Allow()
	err := rl.Allow()
	if err == nil {
		t.Fatal("expected error")
	}
	if !errors.Is(err, ErrRateLimited) {
		t.Errorf("expected ErrRateLimited, got %v", err)
	}
}

// TestConcurrentAllow verifies no data races under concurrent Allow calls
// (contract §7).
func TestConcurrentAllow(t *testing.T) {
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	rl := NewRateLimiterAt(now.Add(-30*24*time.Hour), now)

	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = rl.Allow()
		}()
	}
	wg.Wait()
}

// TestTokenRefillWithSynctest uses testing/synctest to verify that after
// waiting 1/rate seconds, a token is available again (contract §2).
func TestTokenRefillWithSynctest(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		now := time.Now()
		rl := NewRateLimiterAt(now.Add(-30*24*time.Hour), now) // warmup 1.0

		// Exhaust per-second burst (2 tokens).
		for i := 0; i < 2; i++ {
			if err := rl.Allow(); err != nil {
				t.Fatalf("Allow() #%d: unexpected error: %v", i+1, err)
			}
		}
		// Should be rejected now.
		if err := rl.Allow(); err == nil {
			t.Fatal("expected rejection after burst exhaustion")
		}

		// Wait for 1 token to refill (per-second rate is 2/s → 500ms per token).
		time.Sleep(600 * time.Millisecond)

		// Should allow one more.
		if err := rl.Allow(); err != nil {
			t.Fatalf("Allow() after refill: unexpected error: %v", err)
		}
	})
}
