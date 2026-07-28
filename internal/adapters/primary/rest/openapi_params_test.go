package rest

import (
	"encoding/json"
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/yolo-labz/wa/v2/internal/agentdocs"
)

// TestQueryParamsAreDocumented asserts every query parameter these
// handlers read is one openapi.json publishes.
//
// The SSE stream honoured ?since= as its ring-resume cursor while
// openapi.json listed no parameters at all, so the one call an agent
// needs to reconnect without losing events was undiscoverable from the
// published contract. Nothing failed loudly — the daemon just silently
// tailed from the head and the agent lost the gap.
//
// Reading the source rather than exercising the handlers is deliberate:
// a behavioural test can only check the params it already knows to
// send, which is exactly the knowledge that goes missing when someone
// adds a parameter and forgets the catalog.
func TestQueryParamsAreDocumented(t *testing.T) {
	t.Parallel()

	documented := documentedParamNames(t)
	fset := token.NewFileSet()
	paths, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatalf("list package files: %v", err)
	}
	if len(paths) == 0 {
		t.Fatal("no source files found — the guard would pass vacuously")
	}

	for _, path := range paths {
		if strings.HasSuffix(path, "_test.go") {
			continue
		}
		file, err := parser.ParseFile(fset, path, nil, 0)
		if err != nil {
			t.Fatalf("parse %s: %v", path, err)
		}
		ast.Inspect(file, func(n ast.Node) bool {
			name, ok := queryParamName(n)
			if !ok {
				return true
			}
			if !documented[name] {
				t.Errorf("%s reads query parameter %q, which openapi.json does not document",
					fset.Position(n.Pos()), name)
			}
			return true
		})
	}
}

// queryParamName matches `<expr>.Query().Get("<name>")` and returns the
// literal name.
func queryParamName(n ast.Node) (string, bool) {
	call, ok := n.(*ast.CallExpr)
	if !ok || len(call.Args) != 1 {
		return "", false
	}
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok || sel.Sel.Name != "Get" {
		return "", false
	}
	inner, ok := sel.X.(*ast.CallExpr)
	if !ok {
		return "", false
	}
	innerSel, ok := inner.Fun.(*ast.SelectorExpr)
	if !ok || innerSel.Sel.Name != "Query" {
		return "", false
	}
	lit, ok := call.Args[0].(*ast.BasicLit)
	if !ok || lit.Kind != token.STRING {
		return "", false
	}
	name, err := strconv.Unquote(lit.Value)
	if err != nil {
		return "", false
	}
	return name, true
}

// documentedParamNames collects every parameter name openapi.json
// declares, across all paths and locations. Deliberately not scoped per
// endpoint: mapping a handler function back to its route would mean
// re-implementing the mux in a test, and a name published on any
// endpoint is at least a name someone had to think about.
func documentedParamNames(t *testing.T) map[string]bool {
	t.Helper()
	var doc struct {
		Paths map[string]map[string]struct {
			Parameters []struct {
				Name string `json:"name"`
			} `json:"parameters"`
		} `json:"paths"`
	}
	if err := json.Unmarshal(agentdocs.OpenAPIJSON, &doc); err != nil {
		t.Fatalf("openapi.json invalid: %v", err)
	}
	out := map[string]bool{}
	for _, ops := range doc.Paths {
		for _, op := range ops {
			for _, p := range op.Parameters {
				out[p.Name] = true
			}
		}
	}
	return out
}
