// Package main — end-to-end tests for `wa export`. Covers #177: a chat
// with no rows must exit non-zero with a stderr warning instead of the old
// silent exit-0 that made every JID-guess miss look like a successful empty
// export.
package main

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestWaExport_EmptyChatExitsNonZero is the #177 regression. The fake daemon
// returns zero messages (the same response a typo'd or never-seen JID yields,
// since the local store has no chats table). The CLI must NOT exit 0 silently.
func TestWaExport_EmptyChatExitsNonZero(t *testing.T) {
	fd := newFakeDaemon(t)
	fd.on("export", func(_ json.RawMessage) (any, *rpcError) {
		return map[string]any{"messages": []any{}}, nil
	})

	stdout, stderr := runCmd(t,
		"--socket", fd.path(),
		"export", "--chat", "5581998398677@s.whatsapp.net",
	)

	if strings.TrimSpace(stdout) != "" {
		t.Fatalf("stdout must stay empty on a miss, got %q", stdout)
	}
	// runCmd appends "[exec error: ...]" to stderr when the command returns
	// a non-nil error — its proxy for a non-zero process exit.
	if !strings.Contains(stderr, "exec error") {
		t.Fatalf("expected non-zero exit on empty export (#177), stderr=%q", stderr)
	}
	if !strings.Contains(stderr, "0 messages") {
		t.Fatalf("expected an explanatory stderr message, got %q", stderr)
	}
}

// TestWaExport_NonEmptyChatPrintsNDJSON guards the happy path: a chat with
// messages still streams NDJSON to stdout and exits 0.
func TestWaExport_NonEmptyChatPrintsNDJSON(t *testing.T) {
	fd := newFakeDaemon(t)
	fd.on("export", func(_ json.RawMessage) (any, *rpcError) {
		return map[string]any{"messages": []any{
			map[string]any{
				"messageId": "msg_01",
				"chatJid":   "5581998398677@s.whatsapp.net",
				"senderJid": "5581998398677@s.whatsapp.net",
				"timestamp": 1700000000,
				"body":      "hello",
				"isFromMe":  false,
			},
		}}, nil
	})

	stdout, stderr := runCmd(t,
		"--socket", fd.path(),
		"export", "--chat", "5581998398677@s.whatsapp.net",
	)

	if strings.Contains(stderr, "exec error") {
		t.Fatalf("happy-path export must exit 0, stderr=%q", stderr)
	}
	if !strings.Contains(stdout, `"messageId":"msg_01"`) {
		t.Fatalf("stdout missing the exported message: %q", stdout)
	}
	if !strings.Contains(stdout, `"schema":"wa.export/v1"`) {
		t.Fatalf("stdout missing the NDJSON schema envelope: %q", stdout)
	}
}
