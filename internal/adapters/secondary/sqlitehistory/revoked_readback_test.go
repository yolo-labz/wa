package sqlitehistory

// A revoked message must read back AS revoked.
//
// The column has existed since migration v4 and Moderator.Revoke has been
// stamping it since feature 115, but nothing selected it back: `wa history`
// and `wa export` projected the original text of a revoked message, so a
// successful revoke (audit `action=revoke decision=ok`) still read as
// "message is live" to every consumer of the read path (02/11/2026).

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/yolo-labz/wa/v2/internal/domain"
)

func openRevokeTestStore(t *testing.T) (*Store, context.Context) {
	t.Helper()
	ctx := context.Background()
	st, err := Open(ctx, filepath.Join(t.TempDir(), "messages.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return st, ctx
}

func TestRevokedMessageReadsBackWithRevokedAt(t *testing.T) {
	st, ctx := openRevokeTestStore(t)
	const (
		chatID = "215178096468120@lid"
		msgID  = "3EB0AA896AA2A9115790DE"
		live   = "Marcela, confirmando nossa call quinta 05/11 às 14h."
	)
	if err := st.Insert(ctx, []StoredMessage{{
		ChatJID:   chatID,
		SenderJID: chatID,
		MessageID: msgID,
		Timestamp: 1793630240,
		Body:      live,
		IsFromMe:  true,
	}}); err != nil {
		t.Fatalf("Insert: %v", err)
	}

	before, err := st.QueryHistory(ctx, chatID, "", 10)
	if err != nil || len(before) != 1 {
		t.Fatalf("QueryHistory before revoke = %v, %v", before, err)
	}
	if before[0].RevokedAt != 0 {
		t.Fatalf("RevokedAt before the revoke = %d, want 0", before[0].RevokedAt)
	}

	const revokedAt int64 = 1793645274
	if err := st.StampRevoke(
		ctx, domain.MustJID(chatID), domain.MessageID(msgID), domain.RevokeEveryone, revokedAt,
	); err != nil {
		t.Fatalf("StampRevoke: %v", err)
	}

	after, err := st.QueryHistory(ctx, chatID, "", 10)
	if err != nil || len(after) != 1 {
		t.Fatalf("QueryHistory after revoke = %v, %v", after, err)
	}
	if after[0].RevokedAt != revokedAt {
		t.Fatalf("RevokedAt after the revoke = %d, want %d — the read path dropped the marker, "+
			"which is what made a successful revoke look live", after[0].RevokedAt, revokedAt)
	}
	if after[0].MessageID != msgID {
		t.Errorf("MessageID = %q, want %q — identity must survive the revoke", after[0].MessageID, msgID)
	}
}

// TestExportChatFilteredAlsoCarriesRevokedAt pins the second read path: `wa
// export` builds its rows through ExportChatFiltered, so a marker visible only
// in history would still let an export claim the text is live.
func TestExportChatFilteredAlsoCarriesRevokedAt(t *testing.T) {
	st, ctx := openRevokeTestStore(t)
	const (
		chatID = "27106243248295@lid"
		msgID  = "3EB0421F72EBC201E02ADE"
	)
	if err := st.Insert(ctx, []StoredMessage{{
		ChatJID: chatID, SenderJID: chatID, MessageID: msgID,
		Timestamp: 1793630239, Body: "Oi Kamilly, obrigado pela oferta de indicação.",
		IsFromMe: true,
	}}); err != nil {
		t.Fatalf("Insert: %v", err)
	}
	if err := st.StampRevoke(
		ctx, domain.MustJID(chatID), domain.MessageID(msgID), domain.RevokeEveryone, 1793645283,
	); err != nil {
		t.Fatalf("StampRevoke: %v", err)
	}
	rows, err := st.ExportChatFiltered(ctx, chatID, 0, 0, 10)
	if err != nil || len(rows) != 1 {
		t.Fatalf("ExportChatFiltered = %v, %v", rows, err)
	}
	if rows[0].RevokedAt != 1793645283 {
		t.Fatalf("export RevokedAt = %d, want 1793645283", rows[0].RevokedAt)
	}
}

// TestQuerySearch_ReadsRevokedAt is the regression for the legacy `search`
// RPC: QuerySearch kept its own 13-column projection when the shared scan
// gained revoked_at, so every query with at least one hit failed with
// "expected 13 destination arguments in Scan, not 14" (`wa search`, seen
// 11/11/2026). A search with no hit never scanned, which hid it.
func TestQuerySearch_ReadsRevokedAt(t *testing.T) {
	st, ctx := openRevokeTestStore(t)
	const chatID = "215178096468120@lid"
	if err := st.Insert(ctx, []StoredMessage{
		{ChatJID: chatID, SenderJID: chatID, MessageID: "LIVE", Timestamp: 1793630240, Body: "quarterly invoice"},
		{ChatJID: chatID, SenderJID: chatID, MessageID: "DEAD", Timestamp: 1793630241, Body: "invoice withdrawn"},
	}); err != nil {
		t.Fatalf("Insert: %v", err)
	}
	const revokedAt int64 = 1793645274
	if err := st.StampRevoke(ctx, domain.MustJID(chatID), "DEAD", domain.RevokeEveryone, revokedAt); err != nil {
		t.Fatalf("StampRevoke: %v", err)
	}
	hits, err := st.QuerySearch(ctx, "invoice", 10)
	if err != nil {
		t.Fatalf("QuerySearch with hits: %v", err)
	}
	got := map[string]int64{}
	for _, h := range hits {
		got[h.MessageID] = h.RevokedAt
	}
	if len(got) != 2 || got["LIVE"] != 0 || got["DEAD"] != revokedAt {
		t.Fatalf("hits (id -> revokedAt) = %v, want LIVE:0 DEAD:%d", got, revokedAt)
	}
}
