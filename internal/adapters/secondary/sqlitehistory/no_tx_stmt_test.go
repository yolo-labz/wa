package sqlitehistory

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"strings"
	"testing"
)

// txStmtAllowed are the pre-existing startup-only tx-scoped statements
// (migration v8 backfill, RehydrateEmptyBodies). They share the hazard below
// but are not on the persist path; convert them and delete this list.
var txStmtAllowed = map[string]bool{
	"migrate_v8.go:backfillCaptionFolded": true,
	"rehydrate.go:RehydrateEmptyBodies":   true,
}

// TestNoTxScopedPreparedStatements guards the WA-ADMIN-1-panicfix route: a
// *sql.Stmt prepared on a Tx is not tracked on the conn, so when the Tx
// context ends mid-prepare it can escape Tx.closePrepared and be finalized
// later on a closed conn, where modernc crashes the whole process
// (nil tls). Run SQL with tx.ExecContext, which prepares, steps and
// finalizes inside one driver call. Any Prepare/PrepareContext call outside
// the allowlist fails.
func TestNoTxScopedPreparedStatements(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil || len(files) == 0 {
		t.Fatalf("no sources: %v", err)
	}
	fset := token.NewFileSet()
	checked := 0
	for _, name := range files {
		if strings.HasSuffix(name, "_test.go") {
			continue
		}
		f, err := parser.ParseFile(fset, name, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		checked++
		for _, decl := range f.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Body == nil {
				continue
			}
			ast.Inspect(fn.Body, func(n ast.Node) bool {
				call, ok := n.(*ast.CallExpr)
				if !ok {
					return true
				}
				sel, ok := call.Fun.(*ast.SelectorExpr)
				if !ok || (sel.Sel.Name != "PrepareContext" && sel.Sel.Name != "Prepare") {
					return true
				}
				if !txStmtAllowed[name+":"+fn.Name.Name] {
					t.Errorf("%s: %s calls %s; use tx.ExecContext (see this test)", fset.Position(call.Pos()), fn.Name.Name, sel.Sel.Name)
				}
				return true
			})
		}
	}
	if checked < 5 {
		t.Fatalf("only %d non-test files parsed; the guard is not looking at the package", checked)
	}
}
