package sqlitetuning

import (
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// TotalConfiguredCacheKiBBudget is the ceiling on the SUM of every
// store's SQLite page cache, in KiB.
//
// 72 MiB: 32 for messages.db plus 8 for each of the five side stores.
// Before #359 the same sum was 224 MiB (64 + 5×32) — on a daemon whose
// wa-burocracy cgroup limit is 128 MiB.
const TotalConfiguredCacheKiBBudget = 72 * 1000

// storeCachePragmas is every cache_size pragma the daemon installs, with
// how many stores use it. A new store MUST be added here, which is the
// point: the regression this guards is "somebody added a sixth 32 MiB
// side store and nobody noticed the total".
var storeCachePragmas = []struct {
	pragma string
	stores int
}{
	{HistoryCachePragma, 1},   // messages.db
	{SideStoreCachePragma, 5}, // events, webhooks, contacts, drafts, schedules
}

var cacheSizeRe = regexp.MustCompile(`cache_size\(-(\d+)\)`)

// TestTotalConfiguredCacheWithinBudget is the check issue #359 asks for,
// aimed at the thing that actually regressed.
//
// The existing CI gate measures IDLE RSS, and idle RSS structurally
// cannot catch this: a freshly booted daemon has empty databases and
// never fills a page cache, so it measured 32 MiB both before and after
// this change — identical, while the configured budget went from 224 MiB
// to 72 MiB. Production grew into the old ceiling over two days
// (37 MiB → ~80 MiB) precisely because the cache warms with use.
//
// So the deterministic thing to assert is the budget itself. No timing,
// no allocator behaviour, no runner variance — just "the sum of what we
// told SQLite it may hold".
func TestTotalConfiguredCacheWithinBudget(t *testing.T) {
	t.Parallel()
	total := 0
	for _, p := range storeCachePragmas {
		m := cacheSizeRe.FindStringSubmatch(p.pragma)
		if m == nil {
			t.Fatalf("pragma %q has no cache_size(-N) — the budget cannot be computed", p.pragma)
		}
		kib, err := strconv.Atoi(m[1])
		if err != nil {
			t.Fatalf("cache_size in %q is not a number: %v", p.pragma, err)
		}
		total += kib * p.stores
	}

	t.Logf("configured SQLite cache budget = %d KiB (%d MiB) across %d stores",
		total, total/1024, 6)

	if total > TotalConfiguredCacheKiBBudget {
		t.Errorf("configured cache budget %d KiB (%d MiB) exceeds %d KiB (%d MiB). "+
			"A daemon cgroup can be as small as 128 MiB and modernc's page cache maps OUTSIDE "+
			"the Go heap, so this does not show up in wa.heap.bytes — it shows up as RSS.",
			total, total/1024, TotalConfiguredCacheKiBBudget, TotalConfiguredCacheKiBBudget/1024)
	}
}

// TestCacheBudgetFitsTheSmallestCgroup — the budget has to fit the
// smallest cgroup the daemon actually runs in, alongside the Go heap,
// the whatsmeow session store and the runtime itself. 128 MiB is
// wa-burocracy's real limit, and the old 224 MiB budget did not fit it
// at all — it promised SQLite more memory than the container had.
func TestCacheBudgetFitsTheSmallestCgroup(t *testing.T) {
	t.Parallel()
	smallestCgroupMiB := 128
	budgetMiB := TotalConfiguredCacheKiBBudget / 1024

	if budgetMiB >= smallestCgroupMiB {
		t.Fatalf("cache budget %d MiB does not fit the %d MiB cgroup", budgetMiB, smallestCgroupMiB)
	}
	// GOMEMLIMIT claims memLimitHeadroom of the cgroup for the Go heap.
	// The cache lives OUTSIDE that heap, so budget + soft limit must not
	// together exceed the cgroup, or the two policies contradict.
	softLimitMiB := int(float64(smallestCgroupMiB-budgetMiB) * memLimitHeadroom)
	if budgetMiB+softLimitMiB > smallestCgroupMiB {
		t.Errorf("cache budget %d MiB + GOMEMLIMIT %d MiB exceeds the %d MiB cgroup",
			budgetMiB, softLimitMiB, smallestCgroupMiB)
	}
}

// TestSideStoreDSNCarriesEveryPragma — the five side stores now share one
// DSN builder, so a dropped pragma would silently affect all of them at
// once. WAL and busy_timeout in particular fail late and under load
// rather than at open, which is the worst way to lose a setting.
func TestSideStoreDSNCarriesEveryPragma(t *testing.T) {
	t.Parallel()
	dsn := SideStoreDSN("/tmp/x.db")
	for _, want := range []string{
		"file:/tmp/x.db",
		"_pragma=journal_mode(WAL)",
		"_pragma=synchronous(NORMAL)",
		"_pragma=foreign_keys(ON)",
		"_pragma=busy_timeout(5000)",
		"_pragma=cache_size(-8000)",
		"_pragma=temp_store(MEMORY)",
		"_txlock=immediate",
	} {
		if !strings.Contains(dsn, want) {
			t.Errorf("SideStoreDSN dropped %q: %s", want, dsn)
		}
	}
	// The first pragma must use "?" and the rest "&", or the driver
	// parses the tail as part of the filename.
	if !strings.Contains(dsn, ".db?_pragma=") {
		t.Errorf("DSN query separator malformed: %s", dsn)
	}
	if strings.Count(dsn, "?") != 1 {
		t.Errorf("DSN has %d '?' separators, want exactly 1: %s", strings.Count(dsn, "?"), dsn)
	}
}
