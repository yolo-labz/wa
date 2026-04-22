package observability

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestPprofCPUSeconds1 asserts a 1-second CPU profile produces a
// non-empty protobuf blob. The kind is the critical path for the
// `wa debug pprof cpu` CLI.
func TestPprofCPUSeconds1(t *testing.T) {
	params, _ := json.Marshal(pprofParams{Kind: "cpu", Seconds: 1})

	t0 := time.Now()
	raw, err := PprofHandler(context.Background(), params)
	if err != nil {
		t.Fatalf("PprofHandler: %v", err)
	}
	elapsed := time.Since(t0)

	var res pprofResult
	if err := json.Unmarshal(raw, &res); err != nil {
		t.Fatalf("unmarshal result: %v", err)
	}
	if res.Kind != "cpu" {
		t.Errorf("kind = %q, want %q", res.Kind, "cpu")
	}
	if res.Bytes == 0 {
		t.Errorf("cpu profile empty")
	}
	if elapsed < 800*time.Millisecond {
		t.Errorf("elapsed %s too short for 1s window", elapsed)
	}
	if elapsed > 3*time.Second {
		t.Errorf("elapsed %s too long — 1s window blew past", elapsed)
	}

	b, err := base64.StdEncoding.DecodeString(res.Profile)
	if err != nil {
		t.Fatalf("base64 decode: %v", err)
	}
	if len(b) != res.Bytes {
		t.Errorf("decoded len %d != reported bytes %d", len(b), res.Bytes)
	}
}

// TestPprofHeapSnapshot asserts a heap profile is served as a snapshot
// without blocking for the seconds window. Heap profiles complete in
// sub-second because they walk the live mspan list.
func TestPprofHeapSnapshot(t *testing.T) {
	params, _ := json.Marshal(pprofParams{Kind: "heap"})

	t0 := time.Now()
	raw, err := PprofHandler(context.Background(), params)
	if err != nil {
		t.Fatalf("PprofHandler: %v", err)
	}
	if time.Since(t0) > 2*time.Second {
		t.Errorf("heap snapshot took >2s — should be synchronous")
	}

	var res pprofResult
	if err := json.Unmarshal(raw, &res); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if res.Bytes == 0 {
		t.Errorf("heap profile empty")
	}
}

// TestPprofNoTCPSurface asserts no file under wad's tree imports
// net/http/pprof. That package registers HTTP handlers on the default
// mux, which constitution §IV.21 forbids (no wad-opened TCP listeners).
func TestPprofNoTCPSurface(t *testing.T) {
	// Walk the repo root looking for `net/http/pprof` imports. Start
	// from the repo root (five levels up from this test file's dir).
	repoRoot, err := filepath.Abs("../..")
	if err != nil {
		t.Fatalf("abs repo root: %v", err)
	}

	// Build the forbidden import path at runtime so this test file's
	// own source does not match it via string-scan.
	forbidden := "net/http/" + "pprof"

	var violations []string
	fset := token.NewFileSet()
	err = filepath.Walk(repoRoot, func(path string, info os.FileInfo, werr error) error {
		if werr != nil {
			return werr
		}
		if info.IsDir() {
			switch info.Name() {
			case "vendor", ".git", "node_modules", "testdata":
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") {
			return nil
		}
		f, perr := parser.ParseFile(fset, path, nil, parser.ImportsOnly)
		if perr != nil {
			// Skip files that don't parse cleanly; they can't be
			// actively importing anything either.
			return nil //nolint:nilerr // parse errors on malformed test fixtures are ignored by design
		}
		for _, imp := range f.Imports {
			if imp.Path == nil {
				continue
			}
			if strings.Trim(imp.Path.Value, `"`) == forbidden {
				violations = append(violations, path)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk: %v", err)
	}
	if len(violations) > 0 {
		t.Errorf("net/http/pprof imported — TCP surface forbidden. Files: %v", violations)
	}
}

// TestPprofUnknownKindRejected asserts unknown profile names return
// a typed error rather than hanging or panicking.
func TestPprofUnknownKindRejected(t *testing.T) {
	params, _ := json.Marshal(pprofParams{Kind: "bogus"})
	_, err := PprofHandler(context.Background(), params)
	if err == nil {
		t.Fatalf("expected error for unknown kind")
	}
}

// TestPprofCPUSecondsCap asserts windows >120s are rejected.
func TestPprofCPUSecondsCap(t *testing.T) {
	params, _ := json.Marshal(pprofParams{Kind: "cpu", Seconds: 121})
	_, err := PprofHandler(context.Background(), params)
	if err == nil {
		t.Fatalf("expected error for seconds > %d", pprofMaxSeconds)
	}
}
