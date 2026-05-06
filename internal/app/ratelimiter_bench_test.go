package app

import (
	"testing"
	"time"

	"github.com/yolo-labz/wa/v2/internal/domain"
)

// BenchmarkRateLimiterAllow measures the global rate-limit fast path
// across the three warmup tiers. The middle of each warmup window is
// chosen so the multiplier is unambiguous (25 % on day 4, 50 % on day
// 11, 100 % on day 30). Each iteration calls Allow() once on a freshly
// constructed limiter, so token-bucket exhaustion is not measured —
// the benchmark targets the single-call hot path that runs on every
// outbound RPC. Spec 016 FR-009 / T066.
func BenchmarkRateLimiterAllow(b *testing.B) {
	cases := []struct {
		name    string
		ageDays int
	}{
		{"warmup25", 4},   // day 1-7 → 25 % multiplier
		{"warmup50", 11},  // day 8-14 → 50 %
		{"steady100", 30}, // day 15+  → 100 %
	}
	for _, tc := range cases {
		b.Run(tc.name, func(b *testing.B) {
			created := time.Now().Add(-time.Duration(tc.ageDays) * 24 * time.Hour)
			rl := NewRateLimiter(created)
			b.ResetTimer()
			b.ReportAllocs()
			for range b.N {
				_ = rl.Allow()
			}
		})
	}
}

// BenchmarkRateLimiterAllowFor measures the per-recipient path. A new
// recipient JID is constructed once and reused across iterations so
// the recipient-cap branch is exercised on the hot path. The first
// call seeds the map; subsequent calls hit the existing-key fast path.
// Spec 016 FR-009 / T066.
func BenchmarkRateLimiterAllowFor(b *testing.B) {
	rl := NewRateLimiter(time.Now().Add(-30 * 24 * time.Hour))
	jid, err := domain.Parse("5511999999999@s.whatsapp.net")
	if err != nil {
		b.Fatalf("parse jid: %v", err)
	}
	b.ResetTimer()
	b.ReportAllocs()
	for range b.N {
		_ = rl.AllowFor(jid)
	}
}
