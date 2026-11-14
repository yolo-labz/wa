package sqlitehistory

import (
	"reflect"
	"testing"

	"github.com/yolo-labz/wa/v2/internal/domain"
)

// Every reader that returns StoredMessage rows scans them through
// scanStoredMessages. A reader whose SELECT disagrees with that scan fails
// only when a row comes back, so each one needs a test that gets a hit.
// QuerySearch had none outside the WA_BENCH harness and shipped broken.
const (
	hitChat    = "15551230001@s.whatsapp.net"
	hitRevoked = int64(1793645274)
)

// readerHits gives every column its own value, so a reordered or dropped
// column in a projection shows up as a field mismatch.
func readerHits() map[string]StoredMessage {
	return map[string]StoredMessage{
		"HIT-LIVE": {
			ChatJID: hitChat, SenderJID: "15551230002@s.whatsapp.net", MessageID: "HIT-LIVE", Timestamp: 100,
			Body: "a longer synthetic fixture that mentions the invoice once", MediaType: "image/jpeg",
			Caption: "caption text", PushName: "push name", RawProto: []byte{0x0a, 0x01},
			SenderAltJID: "15551230003@lid", AddressingMode: "pn", InteractiveJSON: []byte(`{"subtype":"list"}`),
		},
		"HIT-REVOKED": {
			ChatJID: hitChat, SenderJID: hitChat, MessageID: "HIT-REVOKED", Timestamp: 200,
			Body: "invoice paid invoice", IsFromMe: true, RevokedAt: hitRevoked,
		},
		"HIT-OTHER": {ChatJID: hitChat, SenderJID: hitChat, MessageID: "HIT-OTHER", Timestamp: 300, Body: "unrelated text"},
	}
}

func seedReaderHits(t *testing.T) *Store {
	t.Helper()
	st, ctx := openRevokeTestStore(t)
	for _, id := range []string{"HIT-LIVE", "HIT-REVOKED", "HIT-OTHER"} {
		m := readerHits()[id]
		m.RevokedAt = 0
		if err := st.Insert(ctx, []StoredMessage{m}); err != nil {
			t.Fatalf("Insert %s: %v", id, err)
		}
	}
	if err := st.StampRevoke(ctx, domain.MustJID(hitChat), "HIT-REVOKED", domain.RevokeEveryone, hitRevoked); err != nil {
		t.Fatalf("StampRevoke: %v", err)
	}
	return st
}

func requireHits(t *testing.T, rows []StoredMessage, err error, wantIDs ...string) {
	t.Helper()
	if err != nil {
		t.Fatalf("reader failed on a hit: %v", err)
	}
	if len(rows) != len(wantIDs) {
		t.Fatalf("rows=%d; want %d", len(rows), len(wantIDs))
	}
	for i, r := range rows {
		if r.MessageID != wantIDs[i] {
			t.Fatalf("row %d = %s; want %s", i, r.MessageID, wantIDs[i])
		}
		if want := readerHits()[r.MessageID]; !reflect.DeepEqual(r, want) {
			t.Fatalf("row scanned into the wrong fields:\n got %+v\nwant %+v", r, want)
		}
	}
}

func TestQuerySearch_HitScansEveryColumn(t *testing.T) {
	st := seedReaderHits(t)
	// The revoked row outranks the live one (two mentions in a short body)
	// but never matches, before LIMIT too: search skips revoked rows.
	rows, err := st.QuerySearch(t.Context(), "invoice", 10)
	requireHits(t, rows, err, "HIT-LIVE")
	one, err := st.QuerySearch(t.Context(), "invoice", 1)
	requireHits(t, one, err, "HIT-LIVE")
	none, err := st.QuerySearch(t.Context(), "absentterm", 10)
	requireHits(t, none, err)
}

func TestQueryMessages_HitScansEveryColumn(t *testing.T) {
	st := seedReaderHits(t)
	rows, err := st.QueryMessages(t.Context(), 10)
	requireHits(t, rows, err, "HIT-OTHER", "HIT-REVOKED", "HIT-LIVE")
}

func TestExportChat_HitScansEveryColumn(t *testing.T) {
	st := seedReaderHits(t)
	rows, err := st.ExportChat(t.Context(), hitChat)
	requireHits(t, rows, err, "HIT-LIVE", "HIT-REVOKED", "HIT-OTHER")
}

func TestQueryChats_HitScansEveryColumn(t *testing.T) {
	st := seedReaderHits(t)
	chats, err := st.QueryChats(t.Context(), 10)
	if err != nil || len(chats) != 1 {
		t.Fatalf("chats=%+v err=%v", chats, err)
	}
	if c := chats[0]; c.ChatJID != hitChat || c.MessageCount != 3 || c.LastMessageTS != 300 || c.IsGroup {
		t.Fatalf("chat summary scanned into the wrong fields: %+v", c)
	}
}
