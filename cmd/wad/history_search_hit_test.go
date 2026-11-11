package main

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/yolo-labz/wa/v2/internal/adapters/secondary/sqlitehistory"
)

// The search RPC answered -32603 for every query that matched a row, because
// no test drove the handler to a hit. Pin the read handlers that had no such
// test at the RPC boundary: a hit must come back as rows.
func TestReadHandlersReturnHits(t *testing.T) {
	ctx := context.Background()
	store, err := sqlitehistory.Open(ctx, filepath.Join(t.TempDir(), "messages.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	const chat = "15551230001@s.whatsapp.net"
	if err := store.Insert(ctx, []sqlitehistory.StoredMessage{
		{ChatJID: chat, SenderJID: chat, MessageID: "HIT-1", Timestamp: 100, Body: "synthetic invoice fixture"},
		{ChatJID: chat, SenderJID: chat, MessageID: "HIT-2", Timestamp: 200, Body: "unrelated text"},
	}); err != nil {
		t.Fatalf("insert: %v", err)
	}
	for name, tc := range map[string]struct {
		handler func(context.Context, json.RawMessage) (json.RawMessage, error)
		params  string
		key     string
		want    int
	}{
		"search":        {makeSearchHandler(store), `{"query":"invoice"}`, "messages", 1},
		"messages":      {makeMessagesHandler(store), `{"limit":10}`, "messages", 2},
		"messages.list": {makeMessagesListHandler(store), `{"chat":"` + chat + `"}`, "messages", 2},
		"chat.list":     {makeChatListHandler(store), `{}`, "chats", 1},
	} {
		t.Run(name, func(t *testing.T) {
			raw, err := tc.handler(ctx, json.RawMessage(tc.params))
			if err != nil {
				t.Fatalf("handler failed on a hit: %v", err)
			}
			var env map[string][]json.RawMessage
			if err := json.Unmarshal(raw, &env); err != nil {
				t.Fatalf("decode: %v", err)
			}
			if len(env[tc.key]) != tc.want {
				t.Fatalf("%s=%d; want %d: %s", tc.key, len(env[tc.key]), tc.want, raw)
			}
		})
	}
}
