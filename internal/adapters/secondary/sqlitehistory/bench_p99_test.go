// Feature 017 T1-22 — bench harness for SC-02 (thread.get ≤ 200 ms p99)
// and SC-03 (messages.search ≤ 500 ms p99) against a 100 000-row fixture.
//
// The fixture lives at $WA_BENCH_DB (default: repo-root bench/testdata/100k_messages.db).
// If it does not exist and WA_BENCH=1 is set, it is generated lazily. The
// file is gitignored (binary artefacts never belong in the repo).
//
// Both tests are gated behind WA_BENCH=1 so the default unit suite stays
// fast; CI opts in via a dedicated bench job. Budgets are the SC values
// from spec.md §Success Criteria.

package sqlitehistory_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"testing"
	"time"

	"github.com/yolo-labz/wa/v2/internal/adapters/secondary/sqlitehistory"
)

const (
	benchFixtureRows  = 100_000
	benchFixtureChats = 200
	benchSampleCount  = 500

	scThreadP99Budget = 200 * time.Millisecond
	scSearchP99Budget = 500 * time.Millisecond
)

// fixturePath resolves the 100K DB path: $WA_BENCH_DB override, else
// <repo-root>/bench/testdata/100k_messages.db, else a tempdir copy.
func fixturePath(tb testing.TB) string {
	tb.Helper()
	if p := os.Getenv("WA_BENCH_DB"); p != "" {
		return p
	}
	_, thisFile, _, _ := runtime.Caller(0)
	repoRoot := filepath.Clean(filepath.Join(filepath.Dir(thisFile), "..", "..", "..", ".."))
	return filepath.Join(repoRoot, "bench", "testdata", "100k_messages.db")
}

// ensureFixture opens or generates the 100K row DB. Returns the opened
// Store — caller must Close.
func ensureFixture(tb testing.TB) *sqlitehistory.Store {
	tb.Helper()
	path := fixturePath(tb)

	if _, err := os.Stat(path); err != nil {
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			tb.Fatalf("mkdir fixture dir: %v", err)
		}
		tb.Logf("seeding 100k fixture at %s (~30s)", path)
		seedFixture(tb, path)
	}

	s, err := sqlitehistory.Open(context.Background(), path)
	if err != nil {
		tb.Fatalf("open fixture: %v", err)
	}
	return s
}

func seedFixture(tb testing.TB, path string) {
	tb.Helper()
	s, err := sqlitehistory.Open(context.Background(), path)
	if err != nil {
		tb.Fatalf("open for seed: %v", err)
	}
	defer func() { _ = s.Close() }()

	const batchSize = 1000
	ctx := context.Background()
	for i := 0; i < benchFixtureRows; i += batchSize {
		end := min(i+batchSize, benchFixtureRows)
		msgs := make([]sqlitehistory.StoredMessage, 0, end-i)
		for j := i; j < end; j++ {
			msgs = append(msgs, sqlitehistory.StoredMessage{
				ChatJID:   fmt.Sprintf("chat%d@s.whatsapp.net", j%benchFixtureChats),
				SenderJID: fmt.Sprintf("sender%d@s.whatsapp.net", j%500),
				MessageID: fmt.Sprintf("msg-%d", j),
				Timestamp: int64(1700000000 + j),
				Body:      fmt.Sprintf("Message body %d lorem ipsum dolor sit amet consectetur testing search phrase %d", j, j%17),
			})
		}
		if err := s.Insert(ctx, msgs); err != nil {
			tb.Fatalf("seed insert batch %d: %v", i, err)
		}
	}
}

// p99 returns the 99th percentile of the samples.
func p99(samples []time.Duration) time.Duration {
	sorted := append([]time.Duration(nil), samples...)
	slices.Sort(sorted)
	idx := max(int(float64(len(sorted))*0.99)-1, 0)
	if idx >= len(sorted) {
		idx = len(sorted) - 1
	}
	return sorted[idx]
}

// TestBenchThreadP99 — SC-02 budget.
func TestBenchThreadP99(t *testing.T) {
	if os.Getenv("WA_BENCH") == "" {
		t.Skip("set WA_BENCH=1 to run 100k-row bench harness")
	}

	s := ensureFixture(t)
	defer func() { _ = s.Close() }()

	ctx := context.Background()
	samples := make([]time.Duration, benchSampleCount)
	for i := range samples {
		chat := fmt.Sprintf("chat%d@s.whatsapp.net", i%benchFixtureChats)
		start := time.Now()
		if _, err := s.QueryHistory(ctx, chat, "", 50); err != nil {
			t.Fatalf("QueryHistory: %v", err)
		}
		samples[i] = time.Since(start)
	}

	got := p99(samples)
	t.Logf("SC-02 thread.get p99 = %s (budget %s, n=%d)", got, scThreadP99Budget, len(samples))
	if got > scThreadP99Budget {
		t.Fatalf("SC-02 violated: p99 %s > %s", got, scThreadP99Budget)
	}
}

// TestBenchSearchP99 — SC-03 budget.
func TestBenchSearchP99(t *testing.T) {
	if os.Getenv("WA_BENCH") == "" {
		t.Skip("set WA_BENCH=1 to run 100k-row bench harness")
	}

	s := ensureFixture(t)
	defer func() { _ = s.Close() }()

	ctx := context.Background()
	terms := []string{"lorem", "ipsum", "testing", "search", "phrase", "message"}
	samples := make([]time.Duration, benchSampleCount)
	for i := range samples {
		q := terms[i%len(terms)]
		start := time.Now()
		if _, err := s.QuerySearch(ctx, q, 20); err != nil {
			t.Fatalf("QuerySearch: %v", err)
		}
		samples[i] = time.Since(start)
	}

	got := p99(samples)
	t.Logf("SC-03 messages.search p99 = %s (budget %s, n=%d)", got, scSearchP99Budget, len(samples))
	if got > scSearchP99Budget {
		t.Fatalf("SC-03 violated: p99 %s > %s", got, scSearchP99Budget)
	}
}
