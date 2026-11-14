package sqlitehistory

import (
	"errors"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/yolo-labz/wa/v2/internal/app"
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

// notReaders are the functions that SELECT message text without serving it:
// schema migrations, and the edit-window probe whose only caller discards
// the body (whatsmeow/moderator.go reads the timestamp).
var notReaders = map[string]bool{"DownV5": true, "backfillCaptionFolded": true, "GetMessageMeta": true}

// textSelect spots a SELECT of body or caption from messages in a literal.
var textSelect = regexp.MustCompile(`(?is)select\b.*\b(body|caption)\b.*\bfrom\s+messages\b`)

// TestScanStoredMessagesUsesSharedProjection (p8 SEARCH-SCAN-FIX L1): every
// non-test function that scans with scanStoredMessages, or selects body or
// caption from messages in a literal of its own (p8 REVOKED-TEXT I2), also
// uses storedMessageSelect or liveBody, so no private projection can serve
// a revoked row's text.
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
			if !ok || fn.Body == nil || fn.Name.Name == "scanStoredMessages" || notReaders[fn.Name.Name] {
				continue
			}
			scans, shared, private := false, false, false
			ast.Inspect(fn.Body, func(n ast.Node) bool {
				switch n := n.(type) {
				case *ast.Ident:
					scans = scans || n.Name == "scanStoredMessages"
					shared = shared || n.Name == "storedMessageSelect" || n.Name == "liveBody"
				case *ast.BasicLit: // a private SELECT of a text column from messages (I2)
					private = private || n.Kind == token.STRING && textSelect.MatchString(n.Value)
				}
				return true
			})
			if (scans || private) && !shared {
				t.Errorf("%s: %s reads message text without storedMessageSelect or liveBody", fset.Position(fn.Pos()), fn.Name.Name)
			}
		}
	}
}

// TestQuerySearch_SkipsRevoked: FTS search never returns a revoked row,
// of either scope, so its dead body cannot be probed through matches.
func TestQuerySearch_SkipsRevoked(t *testing.T) {
	st, ctx := openRevokeTestStore(t)
	chat := domain.MustJID("15551234567@s.whatsapp.net")
	for i, id := range []string{"live", "everyone", "self"} {
		if err := st.InsertRaw(ctx, domain.HistoryRecord{
			ChatJID: chat.String(), SenderJID: chat.String(), MessageID: id, Timestamp: int64(i + 1), Body: "invoice " + id,
		}); err != nil {
			t.Fatal(err)
		}
	}
	for id, scope := range map[domain.MessageID]domain.RevokeScope{"everyone": domain.RevokeEveryone, "self": domain.RevokeSelf} {
		if err := st.StampRevoke(ctx, chat, id, scope, 1_700_000_000); err != nil {
			t.Fatal(err)
		}
	}
	for _, q := range []string{"invoice", "everyone", "self"} {
		got, err := st.QuerySearch(ctx, q, 10)
		if err != nil {
			t.Fatal(err)
		}
		for _, m := range got {
			if m.MessageID != "live" {
				t.Errorf("search %q returned revoked row %s", q, m.MessageID)
			}
		}
	}
}

// TestGetRawProto_RefusesRevoked (p8 REVOKED-TEXT M1/L2): the stored proto
// of a revoked message, either scope, is os.ErrNotExist to every reader of
// it (media download and transcript, interactive quoting, quote
// projection), qualified or not; the live row is untouched.
func TestGetRawProto_RefusesRevoked(t *testing.T) {
	st, ctx := openRevokeTestStore(t)
	chat := domain.MustJID("15551234567@s.whatsapp.net")
	for i, id := range []string{"live", "everyone", "self"} {
		if err := st.InsertRaw(ctx, domain.HistoryRecord{
			ChatJID: chat.String(), SenderJID: chat.String(), MessageID: id, Timestamp: int64(i + 1), RawProto: []byte("proto-" + id),
		}); err != nil {
			t.Fatal(err)
		}
	}
	for id, scope := range map[domain.MessageID]domain.RevokeScope{"everyone": domain.RevokeEveryone, "self": domain.RevokeSelf} {
		if err := st.StampRevoke(ctx, chat, id, scope, 1_700_000_000); err != nil {
			t.Fatal(err)
		}
	}
	quoted := NewQuotedMessageAdapter(st)
	for _, id := range []string{"everyone", "self"} {
		for _, c := range []string{chat.String(), ""} {
			if _, raw, err := st.GetRawProto(ctx, c, id); !errors.Is(err, os.ErrNotExist) || raw != nil {
				t.Errorf("GetRawProto(%q, %s) = %q, %v; want os.ErrNotExist", c, id, raw, err)
			}
		}
		if raw, err := quoted.GetRawProto(ctx, chat, domain.MessageID(id)); !errors.Is(err, app.ErrMessageNotFound) || raw != nil {
			t.Errorf("quoted %s = %q, %v; want ErrMessageNotFound", id, raw, err)
		}
	}
	if _, raw, err := st.GetRawProto(ctx, "", "live"); err != nil || string(raw) != "proto-live" {
		t.Errorf("live row = %q, %v", raw, err)
	}
}
