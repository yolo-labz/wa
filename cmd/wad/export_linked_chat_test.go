package main

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/yolo-labz/wa/v2/internal/adapters/secondary/sqlitehistory"
)

// exportLinkedStore seeds a split conversation: our outbound under the
// phone JID, their reply under the LID. That is the shape WhatsApp
// actually produces for a business contact, and the shape that made an
// export of either half look complete.
func exportLinkedStore(t *testing.T) *sqlitehistory.Store {
	t.Helper()
	ctx := context.Background()
	s, err := sqlitehistory.Open(ctx, filepath.Join(t.TempDir(), "messages.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })

	if err := s.InsertRaw(ctx, exportPN, exportPN, "out-1", 100,
		"tem a bateria?", "", "", "", true, nil, "", ""); err != nil {
		t.Fatalf("seed outbound: %v", err)
	}
	if err := s.InsertRaw(ctx, exportLID, exportLID, "in-1", 200,
		"8ah estou em falta", "", "", "", false, nil, "", ""); err != nil {
		t.Fatalf("seed inbound: %v", err)
	}
	return s
}

const (
	exportPN  = "558198552240@s.whatsapp.net"
	exportLID = "98878921670692@lid"
)

// decodeExport pulls the two fields the caller contract cares about: the
// rows that were returned, and the advisory naming the rows that were not.
func decodeExport(t *testing.T, raw json.RawMessage) (int, string, int) {
	t.Helper()
	var resp struct {
		Messages []json.RawMessage `json:"messages"`
		Linked   *struct {
			Chat     string `json:"chat"`
			Messages int    `json:"messages"`
		} `json:"linked"`
	}
	if err := json.Unmarshal(raw, &resp); err != nil {
		t.Fatalf("decode export result: %v", err)
	}
	if resp.Linked == nil {
		return len(resp.Messages), "", 0
	}
	return len(resp.Messages), resp.Linked.Chat, resp.Linked.Messages
}

// TestExportReportsLinkedChat pins issue #355: exporting one half of a
// PN↔LID conversation must name the other half. Before this, the export
// below returned our single outbound message with exit 0 and no signal
// that the reply existed — three vendors who answered within a minute were
// recorded as never having answered.
func TestExportReportsLinkedChat(t *testing.T) {
	t.Parallel()
	store := exportLinkedStore(t)
	ctx := context.Background()

	// Stands in for the identity resolver: the PN's counterpart is the LID.
	linked := func(_ context.Context, chatJID string) (string, int) {
		if chatJID == exportPN {
			return exportLID, 1
		}
		return "", 0
	}

	h := makeExportHandler(store, linked)
	raw, err := h(ctx, json.RawMessage(`{"chat":"`+exportPN+`"}`))
	if err != nil {
		t.Fatalf("export: %v", err)
	}

	got, linkedChat, linkedCount := decodeExport(t, raw)
	// The advisory must not change which rows come back — a caller piping
	// NDJSON sees exactly what it saw before.
	if got != 1 {
		t.Errorf("messages = %d, want 1 (the note must not alter the payload)", got)
	}
	if linkedChat != exportLID {
		t.Errorf("linked.chat = %q, want %q", linkedChat, exportLID)
	}
	if linkedCount != 1 {
		t.Errorf("linked.messages = %d, want 1", linkedCount)
	}
}

// TestExportOmitsLinkedChatWhenUninformative covers the two silences. A
// daemon with no resolver wired keeps the pre-#355 wire shape exactly, and
// a counterpart that exists but holds nothing is not worth a note — an
// advisory that fires on every export is one operators learn to ignore.
func TestExportOmitsLinkedChatWhenUninformative(t *testing.T) {
	t.Parallel()
	store := exportLinkedStore(t)
	ctx := context.Background()

	cases := []struct {
		name   string
		linked linkedChatFunc
	}{
		{"no resolver wired", nil},
		{"counterpart holds no rows", func(context.Context, string) (string, int) {
			return exportLID, 0
		}},
		{"no counterpart known", func(context.Context, string) (string, int) {
			return "", 0
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			h := makeExportHandler(store, tc.linked)
			raw, err := h(ctx, json.RawMessage(`{"chat":"`+exportPN+`"}`))
			if err != nil {
				t.Fatalf("export: %v", err)
			}
			if _, chat, n := decodeExport(t, raw); chat != "" || n != 0 {
				t.Errorf("linked = {%q, %d}, want absent", chat, n)
			}
		})
	}
}
