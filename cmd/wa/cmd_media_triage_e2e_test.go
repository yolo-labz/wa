package main

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestWaMediaListTriageFlags proves --sender/--caption/--since/--until reach
// the wire. A registered-but-unplumbed flag is silently permissive: the caller
// believes they narrowed to one participant, the daemon still returns the
// whole group, and they fetch attachments that were never theirs. Issue #314.
func TestWaMediaListTriageFlags(t *testing.T) {
	fd := newFakeDaemon(t)
	fd.on("media.list", func(params json.RawMessage) (any, *rpcError) {
		var p struct {
			Chat      string `json:"chat"`
			Sender    string `json:"sender"`
			Caption   string `json:"caption"`
			Since     int64  `json:"since"`
			Until     int64  `json:"until"`
			MediaType string `json:"mediaType"`
		}
		if err := json.Unmarshal(params, &p); err != nil {
			return nil, &rpcError{Code: -32602, Message: err.Error()}
		}
		switch {
		case p.Chat != "120363000000000000@g.us":
			return nil, &rpcError{Code: -32602, Message: "chat = " + p.Chat}
		case p.Sender != "5511900000000@s.whatsapp.net":
			return nil, &rpcError{Code: -32602, Message: "sender = " + p.Sender}
		// The substring travels raw; the daemon owns the LIKE wildcards.
		case p.Caption != "catalogo":
			return nil, &rpcError{Code: -32602, Message: "caption = " + p.Caption}
		case p.Since != 1782864000: // 2026-07-01T00:00:00Z
			return nil, &rpcError{Code: -32602, Message: "since mismatch"}
		case p.Until != 1784073600: // 2026-07-15T00:00:00Z
			return nil, &rpcError{Code: -32602, Message: "until mismatch"}
		}
		return map[string]any{"media": []any{}}, nil
	})

	stdout, stderr := runCmd(
		t,
		"--socket", fd.path(),
		"--json",
		"media", "list",
		"--chat", "120363000000000000@g.us",
		"--sender", "5511900000000@s.whatsapp.net",
		"--caption", "catalogo",
		"--since", "2026-07-01T00:00:00Z",
		"--until", "2026-07-15T00:00:00Z",
	)
	if strings.Contains(stderr, "exec error") || strings.Contains(stderr, "-32602") {
		t.Fatalf("media list failed:\nstdout=%q\nstderr=%q", stdout, stderr)
	}
	calls := fd.seen()
	if len(calls) != 1 || calls[0].Method != "media.list" {
		t.Fatalf("expected 1 media.list call, got %+v", calls)
	}
}

// TestWaMediaListRejectsBadTime keeps the RFC3339 contract shared with
// `wa messages list`: a malformed window is a usage error, never a silently
// unbounded query.
func TestWaMediaListRejectsBadTime(t *testing.T) {
	fd := newFakeDaemon(t)
	fd.on("media.list", func(json.RawMessage) (any, *rpcError) {
		return map[string]any{"media": []any{}}, nil
	})

	_, stderr := runCmd(
		t,
		"--socket", fd.path(),
		"media", "list",
		"--since", "yesterday",
	)
	if !strings.Contains(stderr, "--since must be RFC3339") {
		t.Fatalf("stderr = %q, want the RFC3339 usage error", stderr)
	}
	if calls := fd.seen(); len(calls) != 0 {
		t.Fatalf("a rejected window must not reach the daemon, got %+v", calls)
	}
}
