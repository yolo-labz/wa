package app_test

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// schemaRe matches every schema literal of the shape "wa.<dotted>/vN".
var schemaRe = regexp.MustCompile(`"wa\.[a-z0-9.-]+/v\d+"`)

// TestSchemasGoldenLocked is the drift guard for the JSON-RPC result
// schema strings exposed on the wire. Every `{"schema":"wa.X/vN"}`
// literal in the source tree (internal/, cmd/) must appear in the
// goldenSchemas list below.
//
// Adding a new method → add its `"wa.new-method/v1"` string here.
// Bumping a schema → replace `v1` with `v2` here AND migrate clients.
// The commit that introduces the new/bumped schema MUST update this
// list in the same diff, which produces a reviewable audit trail.
func TestSchemasGoldenLocked(t *testing.T) {
	repoRoot := findRepoRoot(t)

	// Union every literal found under internal/ and cmd/.
	seen := map[string]struct{}{}
	for _, sub := range []string{"internal", "cmd"} {
		err := filepath.WalkDir(filepath.Join(repoRoot, sub), func(path string, d os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() {
				return nil
			}
			if !strings.HasSuffix(path, ".go") {
				return nil
			}
			if strings.HasSuffix(path, "_test.go") {
				return nil
			}
			b, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			for _, m := range schemaRe.FindAllString(string(b), -1) {
				seen[strings.Trim(m, `"`)] = struct{}{}
			}
			return nil
		})
		if err != nil {
			t.Fatalf("walk %s: %v", sub, err)
		}
	}

	got := make([]string, 0, len(seen))
	for s := range seen {
		got = append(got, s)
	}
	sort.Strings(got)

	want := goldenSchemas()
	sort.Strings(want)

	if diff := sliceDiff(got, want); diff != "" {
		t.Fatalf("schema drift detected — update goldenSchemas in this file:\n%s", diff)
	}
}

// goldenSchemas is the committed set of wire schema string LITERALS.
// Any change to this list MUST accompany a client migration note in
// CHANGELOG.md.
//
// Scope: this test only covers schemas declared as string literals in
// non-test source. Dynamic schemas formed via fmt.Sprintf("wa.%s/v1",
// method) in cmd/wa/output.go are exercised by cmd/wa/cli_test.go's
// TestFormatResultJSON/TestRPCCodeToExit table.
func goldenSchemas() []string {
	return []string{
		"wa.admin.audit.rotate/v1",
		"wa.admin.reload/v1",
		"wa.chat.list/v1",
		"wa.crash.list/v1",
		"wa.debug.pprof/v1",
		"wa.doctor/v1",
		"wa.event/v1",
		"wa.export/v1",
		"wa.health/v1",
		"wa.history/v1",
		"wa.media.gc/v1",
		"wa.media.list/v1",
		"wa.media.upload/v1",
		"wa.messages.list/v1",
		"wa.messages/v1",
		"wa.privacy.get/v1",
		"wa.rest.version/v1",
		"wa.search/v1",
		"wa.sync.force/v1",
		"wa.sync.status/v1",
		"wa.version/v1",
	}
}

func sliceDiff(got, want []string) string {
	wantSet := map[string]struct{}{}
	for _, s := range want {
		wantSet[s] = struct{}{}
	}
	gotSet := map[string]struct{}{}
	for _, s := range got {
		gotSet[s] = struct{}{}
	}
	var added, removed []string
	for _, s := range got {
		if _, ok := wantSet[s]; !ok {
			added = append(added, s)
		}
	}
	for _, s := range want {
		if _, ok := gotSet[s]; !ok {
			removed = append(removed, s)
		}
	}
	if len(added) == 0 && len(removed) == 0 {
		return ""
	}
	var sb strings.Builder
	for _, s := range added {
		sb.WriteString("+ ")
		sb.WriteString(s)
		sb.WriteString("\n")
	}
	for _, s := range removed {
		sb.WriteString("- ")
		sb.WriteString(s)
		sb.WriteString("\n")
	}
	return sb.String()
}

// findRepoRoot walks upward from the test's cwd until it sees go.mod.
func findRepoRoot(t *testing.T) string {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	for range 8 {
		if _, err := os.Stat(filepath.Join(wd, "go.mod")); err == nil {
			return wd
		}
		wd = filepath.Dir(wd)
	}
	t.Fatalf("go.mod not found walking up")
	return ""
}
