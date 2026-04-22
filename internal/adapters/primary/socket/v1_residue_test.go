package socket

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestNoV1PathsRemain — FR-012, R-12. The system.hello handshake in
// hello.go rejects any protoVersion != domain.ProtoVersion (=2) at
// accept(2); there is no legitimate v1 dispatch code in this package.
// This test fails fast if a v1-flavoured handler, dispatch branch, or
// compatibility shim re-enters the non-test source — catching the kind
// of "just let v1 through this one time" regression that would defeat
// the hard cutover.
//
// The CI `detect` job runs an equivalent grep across the repo; the two
// are intentionally redundant — one catches pre-push, the other
// catches post-push drift.
func TestNoV1PathsRemain(t *testing.T) {
	// Patterns that unambiguously indicate v1 dispatch. Bare "legacy"
	// is NOT listed because path_darwin.go / path_linux.go use it for
	// feature-008 single-profile socket path back-compat, which is
	// unrelated to JSON-RPC protocol version.
	forbidden := []string{
		`ProtoVersion == 1`,
		`"protoVersion":1`,
		`"protoVersion": 1`,
		`handleV1`,
		`dispatchV1`,
		`v1 dispatch`,
		`protoVersion 1 `,
	}

	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("read socket dir: %v", err)
	}

	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(".", name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		src := string(data)
		for _, bad := range forbidden {
			if strings.Contains(src, bad) {
				t.Errorf("v1 dispatch residue in %s: %q — FR-012/R-12 hard cutover forbids v1 handling outside hello.go rejection", name, bad)
			}
		}
	}
}
