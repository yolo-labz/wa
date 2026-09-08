package sqlitetuning

import (
	"strings"
	"testing"
)

// withCgroupPaths stubs the file-reading seam for one test. It supplies
// CONTENTS, matching readCgroupLimits' shape: the marker "\x00missing"
// means that file does not exist, so it contributes nothing — which is
// what os.ReadFile failing looks like to the caller.
func withCgroupPaths(t *testing.T, contents ...string) {
	t.Helper()
	saved := readCgroupLimits
	t.Cleanup(func() { readCgroupLimits = saved })

	present := make([]string, 0, len(contents))
	for _, c := range contents {
		if c != "\x00missing" {
			present = append(present, c)
		}
	}
	readCgroupLimits = func() []string { return present }
}

// wantLimitFor mirrors MemoryLimit's arithmetic from the same constants,
// so this test cannot go on asserting a formula the implementation no
// longer uses. It is the arithmetic that is under test elsewhere; here
// it is only the plumbing (which path, which fallback).
func wantLimitFor(cgroupBytes int64) int64 {
	avail := cgroupBytes - int64(TotalConfiguredCacheKiB)*1024
	return int64(float64(avail) * memLimitHeadroom)
}

// TestMemoryLimitFromCgroup — the case that matters. wad hardcoded
// 512 MiB, which on the 128 MiB wa-burocracy cgroup was 4x the ceiling,
// so the Go soft limit could not protect the cgroup it lived in.
func TestMemoryLimitFromCgroup(t *testing.T) {
	withCgroupPaths(t, "134217728") // 128 MiB
	got := MemoryLimit()

	want := wantLimitFor(134217728)
	if got != want {
		t.Errorf("MemoryLimit() = %d, want %d", got, want)
	}
	if got >= 134217728 {
		t.Errorf("soft limit %d is not below the cgroup limit — it cannot protect it", got)
	}
	if got >= DefaultMemoryLimit {
		t.Errorf("soft limit %d did not drop below the hardcoded default", got)
	}
}

// TestMemoryLimitFallsBack — every unreadable / unlimited / malformed
// shape must land on the default rather than a bogus tiny limit. A
// daemon that set GOMEMLIMIT to a parse artifact would GC itself to
// death, which is worse than not tuning at all.
func TestMemoryLimitFallsBack(t *testing.T) {
	cases := []struct {
		name    string
		content string
	}{
		{"file missing", "\x00missing"},
		{"cgroup v2 unlimited", "max"},
		{"empty", ""},
		{"whitespace only", "   \n"},
		{"not a number", "banana"},
		{"zero", "0"},
		{"negative", "-1"},
		{"v1 unlimited sentinel", "9223372036854771712"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			withCgroupPaths(t, tc.content)
			if got := MemoryLimit(); got != DefaultMemoryLimit {
				t.Errorf("MemoryLimit() = %d, want the %d default", got, DefaultMemoryLimit)
			}
		})
	}
}

// TestMemoryLimitPrefersFirstReadable — v2 is listed first and wins where
// both exist, because a v2 host can expose the v1 path as a compat mount
// with a different (stale) number.
func TestMemoryLimitPrefersFirstReadable(t *testing.T) {
	withCgroupPaths(t, "134217728", "268435456")
	want := wantLimitFor(134217728)
	if got := MemoryLimit(); got != want {
		t.Errorf("MemoryLimit() = %d, want the first path's %d", got, want)
	}
}

// TestMemoryLimitSkipsToSecondPath — an unreadable first entry must not
// abort the search; a v1-only host has nothing at the v2 path.
func TestMemoryLimitSkipsToSecondPath(t *testing.T) {
	withCgroupPaths(t, "\x00missing", "134217728")
	want := wantLimitFor(134217728)
	if got := MemoryLimit(); got != want {
		t.Errorf("MemoryLimit() = %d, want %d from the second path", got, want)
	}
}

// TestCachePragmasAreWellFormed — these strings are concatenated into a
// DSN, so a typo is a silently-ignored pragma rather than an error, and
// the daemon would run on SQLite's 2 MB default while the comment above
// promised a measured budget.
func TestCachePragmasAreWellFormed(t *testing.T) {
	for name, p := range map[string]string{
		"history":   HistoryCachePragma,
		"sideStore": SideStoreCachePragma,
	} {
		if !strings.HasPrefix(p, "&_pragma=cache_size(-") || !strings.HasSuffix(p, ")") {
			t.Errorf("%s pragma is malformed: %q", name, p)
		}
	}
	// The measured ordering: history must stay the larger of the two.
	if HistoryCachePragma == SideStoreCachePragma {
		t.Error("history and side-store budgets collapsed to the same value")
	}
}
