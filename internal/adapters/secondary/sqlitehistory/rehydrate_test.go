package sqlitehistory

import (
	"testing"

	"github.com/yolo-labz/wa/v2/internal/domain"
)

// h151: rows stored with body="" but a kept raw_proto (business templates the
// decoder did not understand yet) are recovered at start, once, and nothing
// else is touched.
func TestRehydrateEmptyBodies(t *testing.T) {
	st, ctx := openRevokeTestStore(t)
	const chat = "155371096244350@lid"
	rows := []StoredMessage{
		{ChatJID: chat, SenderJID: chat, MessageID: "TPL1", Timestamp: 10, RawProto: []byte("tpl")},
		{ChatJID: chat, SenderJID: chat, MessageID: "UNK1", Timestamp: 11, RawProto: []byte("unknown")},
		{ChatJID: chat, SenderJID: chat, MessageID: "NORAW", Timestamp: 12},
		{ChatJID: chat, SenderJID: chat, MessageID: "LIVE", Timestamp: 13, Body: "texto", RawProto: []byte("tpl")},
		{ChatJID: chat, SenderJID: chat, MessageID: "GONE", Timestamp: 14, RawProto: []byte("tpl")},
	}
	if err := st.Insert(ctx, rows); err != nil {
		t.Fatalf("Insert: %v", err)
	}
	if err := st.StampRevoke(ctx, domain.MustJID(chat), "GONE", domain.RevokeEveryone, 99); err != nil {
		t.Fatalf("StampRevoke: %v", err)
	}
	calls := 0
	decode := func(raw []byte) (string, string, string) {
		calls++
		if string(raw) == "tpl" {
			return "Convite para a entrevista\n[button] Iniciar Entrevista", "", ""
		}
		return "", "", ""
	}

	n, err := st.RehydrateEmptyBodies(ctx, decode)
	if err != nil || n != 1 {
		t.Fatalf("RehydrateEmptyBodies = %d, %v; want 1 row", n, err)
	}
	if calls != 2 { // TPL1 and UNK1: not the row without raw_proto, the live one or the revoked one
		t.Fatalf("decoder called %d times, want 2", calls)
	}
	got := map[string]string{}
	msgs, err := st.QueryHistory(ctx, chat, "", 10)
	if err != nil {
		t.Fatalf("QueryHistory: %v", err)
	}
	for _, m := range msgs {
		got[m.MessageID] = m.Body
	}
	want := map[string]string{
		"TPL1": "Convite para a entrevista\n[button] Iniciar Entrevista",
		"UNK1": "", "NORAW": "", "LIVE": "texto", "GONE": "",
	}
	for id, body := range want {
		if got[id] != body {
			t.Errorf("%s body = %q, want %q", id, got[id], body)
		}
	}

	if n, err := st.RehydrateEmptyBodies(ctx, decode); err != nil || n != 0 {
		t.Fatalf("second pass = %d, %v; want 0 (idempotent)", n, err)
	}
}
