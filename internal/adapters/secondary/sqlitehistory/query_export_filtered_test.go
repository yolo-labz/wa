package sqlitehistory_test

import (
	"context"
	"testing"

	"github.com/yolo-labz/wa/v2/internal/adapters/secondary/sqlitehistory"
)

// insertAt seeds a message at a fixed unix timestamp so the since/until
// window assertions below are deterministic.
func insertAt(t *testing.T, s *sqlitehistory.Store, chat, id string, ts int64) {
	t.Helper()
	if err := s.InsertRaw(context.Background(), chat, chat, id, ts, "body-"+id, "", "", "", false, nil, "", ""); err != nil {
		t.Fatalf("InsertRaw %s@%d: %v", id, ts, err)
	}
}

// TestExportChatFiltered_Bounds pins the #180 item-6 since/until/limit
// filtering. The query uses a 0-sentinel toggle WHERE (`(? = 0 OR ts >= ?)`)
// instead of a concatenated predicate list; this test proves the toggle
// reproduces the intended semantics — a 0 bound is unbounded, a non-zero
// bound filters, the row limit caps oldest-first, and chat scoping holds.
func TestExportChatFiltered_Bounds(t *testing.T) {
	t.Parallel()
	s := openTempStore(t)
	const (
		chatA = "15551230001@s.whatsapp.net"
		chatB = "15559990002@s.whatsapp.net"
	)
	// chatA: three messages at ts 100/200/300; chatB: one at 200 so we can
	// prove the rewrite still scopes to a single chat.
	insertAt(t, s, chatA, "a100", 100)
	insertAt(t, s, chatA, "a200", 200)
	insertAt(t, s, chatA, "a300", 300)
	insertAt(t, s, chatB, "b200", 200)

	ctx := context.Background()
	timestamps := func(ms []sqlitehistory.StoredMessage) []int64 {
		out := make([]int64, len(ms))
		for i, m := range ms {
			out[i] = m.Timestamp
		}
		return out
	}
	assertTS := func(t *testing.T, got, want []int64) {
		t.Helper()
		if len(got) != len(want) {
			t.Fatalf("len = %d %v, want %d %v", len(got), got, len(want), want)
		}
		for i := range want {
			if got[i] != want[i] {
				t.Fatalf("ts[%d] = %d, want %d (got %v)", i, got[i], want[i], got)
			}
		}
	}

	cases := []struct {
		name         string
		since, until int64
		limit        int
		want         []int64 // expected ts, oldest-first
	}{
		{"unbounded", 0, 0, 0, []int64{100, 200, 300}},
		{"since only", 200, 0, 0, []int64{200, 300}},
		{"until only", 0, 200, 0, []int64{100, 200}},
		{"window", 150, 250, 0, []int64{200}},
		{"limit caps oldest-first", 0, 0, 2, []int64{100, 200}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel() // read-only queries on the shared store; safe to run concurrently
			got, err := s.ExportChatFiltered(ctx, chatA, tc.since, tc.until, tc.limit)
			if err != nil {
				t.Fatalf("ExportChatFiltered: %v", err)
			}
			assertTS(t, timestamps(got), tc.want)
		})
	}

	// The chatB row must never leak into chatA's export.
	all, err := s.ExportChatFiltered(ctx, chatA, 0, 0, 0)
	if err != nil {
		t.Fatalf("ExportChatFiltered chatA: %v", err)
	}
	for _, m := range all {
		if m.ChatJID != chatA {
			t.Fatalf("chat scoping broken: got row for %s", m.ChatJID)
		}
	}
}
