package sqlitehistory

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"strings"
	"testing"

	"github.com/yolo-labz/wa/v2/internal/domain"
)

// TestQueryMessagesFiltered_CaptionSkipsRevoked (p8 SEARCH-SCAN-FIX M1): a
// caption filter must not match a revoked row; a hit on the dead caption
// would confirm what it said even with the text blanked.
func TestQueryMessagesFiltered_CaptionSkipsRevoked(t *testing.T) {
	st, ctx := openRevokeTestStore(t)
	chat := domain.MustJID("15551234567@s.whatsapp.net")
	for i, id := range []string{"live", "gone"} {
		if err := st.InsertRaw(ctx, domain.HistoryRecord{
			ChatJID: chat.String(), SenderJID: chat.String(), MessageID: id, Timestamp: int64(i + 1),
			MediaType: "image/jpeg", Caption: "invoice " + id, Body: "invoice " + id,
		}); err != nil {
			t.Fatal(err)
		}
	}
	if err := st.StampRevoke(ctx, chat, "gone", domain.RevokeEveryone, 1_700_000_000); err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{"invoice", "invoice gone"} {
		got, err := st.QueryMessagesFiltered(ctx, MessageFilter{ChatJID: chat.String(), Caption: q})
		if err != nil {
			t.Fatal(err)
		}
		for _, m := range got {
			if m.MessageID == "gone" {
				t.Errorf("caption %q matched the revoked row", q)
			}
		}
	}
}

// TestScanStoredMessagesUsesSharedProjection (p8 SEARCH-SCAN-FIX L1): every
// non-test function that scans with scanStoredMessages also uses
// storedMessageSelect, so no private column list can feed the shared scan.
func TestScanStoredMessagesUsesSharedProjection(t *testing.T) {
	files, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	fset := token.NewFileSet()
	for _, e := range files {
		name := e.Name()
		if !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		f, err := parser.ParseFile(fset, name, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		for _, d := range f.Decls {
			fn, ok := d.(*ast.FuncDecl)
			if !ok || fn.Body == nil || fn.Name.Name == "scanStoredMessages" {
				continue
			}
			scans, shared := false, false
			ast.Inspect(fn.Body, func(n ast.Node) bool {
				if id, ok := n.(*ast.Ident); ok {
					scans = scans || id.Name == "scanStoredMessages"
					shared = shared || id.Name == "storedMessageSelect"
				}
				return true
			})
			if scans && !shared {
				t.Errorf("%s: %s calls scanStoredMessages without storedMessageSelect", fset.Position(fn.Pos()), fn.Name.Name)
			}
		}
	}
}
