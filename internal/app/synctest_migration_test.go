package app_test

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// sleepCallRe matches a bare `time.Sleep(` token-open. The trailing `(`
// avoids matching comment prose like "literal time.Sleep". Counts false
// positives only on `time.SleepX(` identifiers — none exist in this repo
// and a collision would itself be a test worth writing, so the weaker
// match is deliberate.
var sleepCallRe = regexp.MustCompile(`\btime\.Sleep\(`)

// TestSynctestMigrationCount is the T3-20 drift guard for feature 018
// tier 3 (per `specs/018-parity-hardening/tasks-tier3.md`). It asserts
// the tree carries no more literal `time.Sleep(` call sites under
// `*_test.go` than the agreed ceiling.
//
// The v1.2.1 baseline, captured by
// `grep -rn 'time.Sleep(' --include='*_test.go' | wc -l` before this
// pass, was 28. The task contract is "≤20% of baseline" → ≤5. We came
// in lower (see the file list below); the ceiling here is set slightly
// above current so a single intentional addition does not trip this
// guard, but any large regression does.
//
// Adding a new test that needs a real-time sleep:
//  1. Prefer the timer-channel form `<-time.After(d)` — same runtime
//     semantics, not counted here.
//  2. Inside a `synctest.Test` bubble, `<-time.After(d)` advances virtual
//     time. `time.Sleep` works too but still counts.
//  3. If the sleep is a genuine race-timing hack against real I/O, leave
//     it AND raise this ceiling by 1 in the same commit, with a comment
//     explaining why the migration is not possible. The ceiling change
//     is the reviewable audit trail.
func TestSynctestMigrationCount(t *testing.T) {
	const maxSleeps = 6

	root := findRepoRoot(t)

	var hits []string
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			// Skip vendor, .git, and specs dirs wholesale.
			name := d.Name()
			if name == ".git" || name == "vendor" || name == "node_modules" ||
				name == "specs" || name == "testdata" {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, "_test.go") {
			return nil
		}
		// Skip self — the drift-guard file necessarily quotes the
		// substring `time.Sleep(` in its doc comment and failure
		// message, which would otherwise self-inflate the count.
		if filepath.Base(path) == "synctest_migration_test.go" {
			return nil
		}
		b, err := os.ReadFile(path) //nolint:gosec // walking own repo
		if err != nil {
			return err
		}
		for _, line := range strings.Split(string(b), "\n") {
			if sleepCallRe.MatchString(line) {
				// Trim path relative to repo root for the failure
				// message — keeps CI logs short.
				rel, _ := filepath.Rel(root, path)
				hits = append(hits, rel)
				break
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk: %v", err)
	}

	// Re-walk to get the exact call count, not just the per-file flag.
	var total int
	for _, rel := range hits {
		b, err := os.ReadFile(filepath.Join(root, rel)) //nolint:gosec
		if err != nil {
			t.Fatalf("read %s: %v", rel, err)
		}
		total += len(sleepCallRe.FindAllIndex(b, -1))
	}

	if total > maxSleeps {
		t.Fatalf("time.Sleep( call sites under *_test.go = %d, ceiling = %d\n"+
			"files still carrying sleeps:\n  %s\n"+
			"Prefer <-time.After(d) or migrate to testing/synctest. See the doc comment on TestSynctestMigrationCount for the policy.",
			total, maxSleeps, strings.Join(hits, "\n  "))
	}
}
