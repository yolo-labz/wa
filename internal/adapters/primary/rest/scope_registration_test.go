package rest

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// registerRe finds every d.RegisterMethod("name", …) in cmd/wad.
var registerRe = regexp.MustCompile(`RegisterMethod\(\s*"([^"]+)"`)

// TestEveryRegisteredMethodIsClassified closes the gap that shipped
// appstate.resync unreachable.
//
// AllowedScope FAILS CLOSED on a method missing from MethodScopes, so an
// unclassified method returns 403 to every caller — including an admin
// token. That is the safe direction, and it is also silent: nothing fails
// at build or test time, the method simply never works. appstate.resync
// shipped that way and was only caught by calling it against production.
//
// The existing scope tests assert properties of the map. This asserts the
// map covers what the daemon actually REGISTERS, which is the thing that
// went wrong.
func TestEveryRegisteredMethodIsClassified(t *testing.T) {
	t.Parallel()
	root := repoRootFromTest(t)
	wad := filepath.Join(root, "cmd", "wad")

	entries, err := os.ReadDir(wad)
	if err != nil {
		t.Fatalf("read %s: %v", wad, err)
	}

	registered := map[string]string{} // method -> file
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".go") || strings.HasSuffix(e.Name(), "_test.go") {
			continue
		}
		b, err := os.ReadFile(filepath.Join(wad, e.Name()))
		if err != nil {
			t.Fatalf("read %s: %v", e.Name(), err)
		}
		for _, m := range registerRe.FindAllStringSubmatch(string(b), -1) {
			registered[m[1]] = e.Name()
		}
	}
	if len(registered) == 0 {
		t.Fatal("found no RegisterMethod calls — the regex or the layout moved")
	}

	var missing []string
	for method, file := range registered {
		if _, ok := MethodScopes[method]; !ok {
			missing = append(missing, method+" (registered in cmd/wad/"+file+")")
		}
	}
	sort.Strings(missing)
	for _, m := range missing {
		t.Errorf("%s is registered but absent from MethodScopes — AllowedScope fails closed, "+
			"so it returns 403 to EVERY caller including admin, silently", m)
	}
}

func repoRootFromTest(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	for range 10 {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	t.Fatal("go.mod not found")
	return ""
}
