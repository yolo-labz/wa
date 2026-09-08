package main

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// xFlagRe matches an `-X main.<symbol>=` stamp in a build config.
var xFlagRe = regexp.MustCompile(`-X\s+main\.([A-Za-z_][A-Za-z0-9_]*)\s*=`)

// TestLdflagTargetsExist is the drift guard for issue #365.
//
// The Go linker SILENTLY IGNORES an `-X main.foo=bar` when package main
// declares no `foo`. Both build systems have always stamped
// `-X main.commit` and `-X main.date` into ./cmd/wa, which declared
// neither — so every wa binary ever built reported a version and no
// commit, and an operator confirming a canary rollout had to `docker cp`
// the binary out and run `strings` on it looking for a literal unique to
// the change they hoped was deployed.
//
// Nothing failed, which is exactly why it lasted. This test is the thing
// that fails.
func TestLdflagTargetsExist(t *testing.T) {
	t.Parallel()
	repo := repoRoot(t)

	for _, cfg := range []struct {
		file string
		pkgs map[string]string // build target -> package dir
	}{
		{
			file: ".goreleaser.yaml",
			pkgs: map[string]string{"./cmd/wa": "cmd/wa", "./cmd/wad": "cmd/wad"},
		},
		{
			file: "Dockerfile",
			pkgs: map[string]string{"./cmd/wa": "cmd/wa", "./cmd/wad": "cmd/wad"},
		},
	} {
		raw, err := os.ReadFile(filepath.Join(repo, cfg.file))
		if err != nil {
			t.Fatalf("read %s: %v", cfg.file, err)
		}
		stamped := stampedSymbols(string(raw))
		if len(stamped) == 0 {
			t.Fatalf("%s: found no -X main.* stamps — the regex or the file moved", cfg.file)
		}

		// Every config here stamps both binaries with one shared ldflag
		// set, so every symbol must exist in every target it builds.
		for target, dir := range cfg.pkgs {
			if !strings.Contains(string(raw), target) {
				continue
			}
			declared := mainVars(t, filepath.Join(repo, dir))
			for _, sym := range stamped {
				if !declared[sym] {
					t.Errorf("%s stamps -X main.%s into %s, but %s declares no such package-level var — the linker drops it silently",
						cfg.file, sym, target, dir)
				}
			}
		}
	}
}

// stampedSymbols returns the sorted, de-duplicated `main.<sym>` names a
// build config stamps.
func stampedSymbols(content string) []string {
	seen := map[string]bool{}
	for _, m := range xFlagRe.FindAllStringSubmatch(content, -1) {
		seen[m[1]] = true
	}
	out := make([]string, 0, len(seen))
	for s := range seen {
		out = append(out, s)
	}
	sort.Strings(out)
	return out
}

// mainVars returns the package-level var names declared in dir. Only vars
// are eligible: the linker's -X can write a string var, and nothing else.
//
// Every non-test .go file is parsed regardless of build tags. That is
// deliberate — a var declared only under some tag still satisfies the
// linker on the builds where it exists, and this guard should fail on a
// symbol that is missing EVERYWHERE, not on one that is merely
// platform-scoped.
func mainVars(t *testing.T, dir string) map[string]bool {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read %s: %v", dir, err)
	}
	fset := token.NewFileSet()
	out := map[string]bool{}
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		f, err := parser.ParseFile(fset, filepath.Join(dir, name), nil, 0)
		if err != nil {
			t.Fatalf("parse %s: %v", name, err)
		}
		for _, decl := range f.Decls {
			gd, ok := decl.(*ast.GenDecl)
			if !ok || gd.Tok != token.VAR {
				continue
			}
			for _, spec := range gd.Specs {
				vs, ok := spec.(*ast.ValueSpec)
				if !ok {
					continue
				}
				for _, n := range vs.Names {
					out[n.Name] = true
				}
			}
		}
	}
	return out
}

// repoRoot walks up from the test's directory to the module root.
func repoRoot(t *testing.T) string {
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
	t.Fatal("go.mod not found walking up from the test directory")
	return ""
}
