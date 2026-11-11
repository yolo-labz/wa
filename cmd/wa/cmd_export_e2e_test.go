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

// TestExport_TimeFieldsCarryUnits (spec 116 S8, F7): the NDJSON export keeps
// the daemon's unit-carrying time pair, has_caption and the wrapped inbound
// caption verbatim, next to the deprecated timestamp, under wa.export/v1.
func TestExport_TimeFieldsCarryUnits(t *testing.T) {
	const wrapped = `<channel source="wa" chat="5581998398677@s.whatsapp.net" sender="5581998398677@s.whatsapp.net" ts="1713888000"><field name="caption">c&lt;1</field></channel>`
	fd := newFakeDaemon(t)
	fd.on("export", func(_ json.RawMessage) (any, *rpcError) {
		return map[string]any{"messages": []any{map[string]any{
			"messageId": "msg_01", "chatJid": "5581998398677@s.whatsapp.net",
			"timestamp": 1713888000, "timestamp_unix_s": 1713888000, "timestamp_at": "2024-04-23T16:00:00Z",
			"has_caption": true, "caption": wrapped, "isFromMe": false,
		}}}, nil
	})
	stdout, stderr := runCmd(t, "--socket", fd.path(), "export", "--chat", "5581998398677@s.whatsapp.net")
	if strings.Contains(stderr, "exec error") {
		t.Fatalf("export must exit 0, stderr=%q", stderr)
	}
	var row map[string]any
	if err := json.Unmarshal([]byte(strings.TrimSpace(stdout)), &row); err != nil {
		t.Fatalf("one NDJSON line expected: %v\n%s", err, stdout)
	}
	for k, want := range map[string]any{
		"schema": "wa.export/v1", "timestamp": float64(1713888000), "timestamp_unix_s": float64(1713888000),
		"timestamp_at": "2024-04-23T16:00:00Z", "has_caption": true, "caption": wrapped,
	} {
		if row[k] != want {
			t.Errorf("%s = %v, want %v", k, row[k], want)
		}
	}
	if strings.Contains(stdout, "e+09") {
		t.Errorf("seconds re-encoded in exponent form: %s", stdout)
	}
}
