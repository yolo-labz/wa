package main

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	wmAdapter "github.com/yolo-labz/wa/v2/internal/adapters/secondary/whatsmeow"
	"github.com/yolo-labz/wa/v2/internal/app"
)

// A handler that returns a bare error gets -32603 "Internal error" at the
// socket boundary — the code that tells a caller "the daemon broke, retry
// later". A missing or malformed parameter is the opposite: the caller
// broke, and retrying the same request will fail identically. These
// handlers all returned bare errors, so `wa export` with no --chat was
// indistinguishable on the wire from a database failure.
//
// The store and adapter are nil/zero on purpose: every case here must be
// refused during param validation, before any dependency is touched. A
// panic in this suite means validation moved after the work.
func TestHandlerParamErrorsCarryInvalidParams(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		handler func(context.Context, json.RawMessage) (json.RawMessage, error)
		params  string
		detail  string
	}{
		{"history missing chat", makeHistoryHandler(nil), `{}`, "chat is required"},
		{"history malformed json", makeHistoryHandler(nil), `{"chat":`, ""},
		{"search missing query", makeSearchHandler(nil), `{}`, "query is required"},
		{"search malformed json", makeSearchHandler(nil), `{"query":`, ""},
		{"purge missing chat", makePurgeHandler(nil, discardLogger()), `{}`, "chat is required"},
		{"export missing chat", makeExportHandler(nil), `{}`, "chat is required"},
		{"export malformed json", makeExportHandler(nil), `{"chat":`, ""},
		{"messages.list malformed json", makeMessagesListHandler(nil), `{"limit":`, ""},
		{"media.list malformed json", makeMediaListHandler(nil, nil), `{"limit":`, ""},
		{"sync.force malformed json", makeSyncForceHandler(&wmAdapter.Adapter{}), `{"count":`, ""},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, err := tc.handler(context.Background(), json.RawMessage(tc.params))
			if err == nil {
				t.Fatal("expected a refusal, got nil")
			}
			code, ok := app.IsCodedError(err)
			if !ok {
				t.Fatalf("error carries no RPC code, so the socket maps it to -32603: %v", err)
			}
			if code != -32602 {
				t.Fatalf("code = %d, want -32602 (invalid params): %v", code, err)
			}
			if tc.detail != "" && !strings.Contains(err.Error(), tc.detail) {
				t.Errorf("message %q drops the detail %q — the caller cannot tell which param is wrong",
					err.Error(), tc.detail)
			}
		})
	}
}

// TestSyncForceInvalidChatCode pins the one param error that is NOT
// -32602: a syntactically present but unparseable JID is -32015, the same
// code every other method uses for a bad JID.
func TestSyncForceInvalidChatCode(t *testing.T) {
	t.Parallel()
	h := makeSyncForceHandler(&wmAdapter.Adapter{})
	_, err := h(context.Background(), json.RawMessage(`{"chat":"abc@s.whatsapp.net"}`))
	if err == nil {
		t.Fatal("expected a refusal on an unparseable chat JID, got nil")
	}
	code, ok := app.IsCodedError(err)
	if !ok || code != -32015 {
		t.Fatalf("code = %d (coded=%v), want -32015 (invalid JID): %v", code, ok, err)
	}
}
