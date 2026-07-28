// Package main — end-to-end coverage for the CLI subcommands the 018
// audit found undriven by any e2e test (TEST-10): react, markRead,
// presence, history, search, thread, wait, session, allow, panic,
// health, sync, webhook, sendMedia, audit.
//
// Each test stands up the in-process fake JSON-RPC daemon on a
// throwaway unix socket (cmd_t121_e2e_test harness), invokes the cobra
// tree via runCmd, and asserts the exact wire method + params the
// subcommand emits — the same convention every other *_e2e_test.go in
// this package follows.
package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestWaReactEmitsRPC exercises `wa react` happy path: exactly one
// `react` RPC with chat/messageId/emoji and the optional idempotency
// key forwarded.
func TestWaReactEmitsRPC(t *testing.T) {
	fd := newFakeDaemon(t)
	fd.on("react", func(params json.RawMessage) (any, *rpcError) {
		var p struct {
			Chat           string `json:"chat"`
			MessageID      string `json:"messageId"`
			Emoji          string `json:"emoji"`
			IdempotencyKey string `json:"idempotencyKey"`
		}
		if err := json.Unmarshal(params, &p); err != nil {
			return nil, &rpcError{Code: -32602, Message: err.Error()}
		}
		if p.Chat != "5511900000000@s.whatsapp.net" || p.MessageID != "m-1" || p.Emoji != "👍" {
			return nil, &rpcError{Code: -32602, Message: "wrong params"}
		}
		if p.IdempotencyKey != "k-react-1" {
			return nil, &rpcError{Code: -32602, Message: "idempotencyKey not forwarded"}
		}
		return map[string]any{}, nil
	})

	_, stderr := runCmd(
		t,
		"--socket", fd.path(),
		"react",
		"--chat", "5511900000000@s.whatsapp.net",
		"--messageId", "m-1",
		"--emoji", "👍",
		"--idempotency-key", "k-react-1",
	)
	if strings.Contains(stderr, "exec error") {
		t.Fatalf("react failed: stderr=%q", stderr)
	}
	calls := fd.seen()
	if len(calls) != 1 || calls[0].Method != "react" {
		t.Fatalf("expected 1 react call, got %+v", calls)
	}
}

// TestWaReactUsageError: missing required flags exits with a usage
// error and never dials the daemon.
func TestWaReactUsageError(t *testing.T) {
	fd := newFakeDaemon(t)
	_, stderr := runCmd(t, "--socket", fd.path(), "react", "--chat", "x@s.whatsapp.net")
	if !strings.Contains(stderr, "--chat and --messageId are required") {
		t.Fatalf("expected usage error, stderr=%q", stderr)
	}
	if n := len(fd.seen()); n != 0 {
		t.Fatalf("usage error must not emit RPCs, saw %d", n)
	}
}

// TestWaMarkReadEmitsRPC exercises `wa markRead` happy path.
func TestWaMarkReadEmitsRPC(t *testing.T) {
	fd := newFakeDaemon(t)
	fd.on("markRead", func(params json.RawMessage) (any, *rpcError) {
		var p struct {
			Chat      string `json:"chat"`
			MessageID string `json:"messageId"`
		}
		if err := json.Unmarshal(params, &p); err != nil {
			return nil, &rpcError{Code: -32602, Message: err.Error()}
		}
		if p.Chat != "5511900000000@s.whatsapp.net" || p.MessageID != "m-2" {
			return nil, &rpcError{Code: -32602, Message: "wrong params"}
		}
		return map[string]any{}, nil
	})

	_, stderr := runCmd(
		t,
		"--socket", fd.path(),
		"markRead",
		"--chat", "5511900000000@s.whatsapp.net",
		"--messageId", "m-2",
	)
	if strings.Contains(stderr, "exec error") {
		t.Fatalf("markRead failed: stderr=%q", stderr)
	}
	calls := fd.seen()
	if len(calls) != 1 || calls[0].Method != "markRead" {
		t.Fatalf("expected 1 markRead call, got %+v", calls)
	}
}

// TestWaSendSeenEmitsRPC exercises `wa sendSeen` happy path: chat-only
// params on the wire, anchored messageId echoed back.
func TestWaSendSeenEmitsRPC(t *testing.T) {
	fd := newFakeDaemon(t)
	fd.on("sendSeen", func(params json.RawMessage) (any, *rpcError) {
		var p struct {
			Chat string `json:"chat"`
		}
		if err := json.Unmarshal(params, &p); err != nil {
			return nil, &rpcError{Code: -32602, Message: err.Error()}
		}
		if p.Chat != "5511900000000@s.whatsapp.net" {
			return nil, &rpcError{Code: -32602, Message: "wrong params"}
		}
		return map[string]any{"messageId": "IN-9"}, nil
	})

	stdout, stderr := runCmd(
		t,
		"--socket", fd.path(),
		"sendSeen",
		"--chat", "5511900000000@s.whatsapp.net",
	)
	if strings.Contains(stderr, "exec error") {
		t.Fatalf("sendSeen failed: stderr=%q", stderr)
	}
	if !strings.Contains(stdout, "IN-9") {
		t.Fatalf("expected anchored messageId in output, stdout=%q", stdout)
	}
	calls := fd.seen()
	if len(calls) != 1 || calls[0].Method != "sendSeen" {
		t.Fatalf("expected 1 sendSeen call, got %+v", calls)
	}
}

// TestWaSendSeenUsageError: missing --chat exits with a usage error and
// never dials the daemon.
func TestWaSendSeenUsageError(t *testing.T) {
	fd := newFakeDaemon(t)
	_, stderr := runCmd(t, "--socket", fd.path(), "sendSeen")
	if !strings.Contains(stderr, "--chat is required") {
		t.Fatalf("expected usage error, stderr=%q", stderr)
	}
	if n := len(fd.seen()); n != 0 {
		t.Fatalf("usage error must not emit RPCs, saw %d", n)
	}
}

// TestWaPresenceComposingAndRecording maps the four-leaf presence tree
// onto its wire methods: composing start + recording stop here cover
// both branches (start/stop share runPresence).
func TestWaPresenceComposingAndRecording(t *testing.T) {
	fd := newFakeDaemon(t)
	assertChat := func(params json.RawMessage) (any, *rpcError) {
		var p struct {
			Chat string `json:"chat"`
		}
		if err := json.Unmarshal(params, &p); err != nil {
			return nil, &rpcError{Code: -32602, Message: err.Error()}
		}
		if p.Chat != "5511900000000@s.whatsapp.net" {
			return nil, &rpcError{Code: -32602, Message: "wrong chat"}
		}
		return map[string]any{}, nil
	}
	fd.on("presence.composing.start", assertChat)
	fd.on("presence.recording.stop", assertChat)

	_, stderr := runCmd(t, "--socket", fd.path(),
		"presence", "composing", "start", "--chat", "5511900000000@s.whatsapp.net")
	if strings.Contains(stderr, "exec error") {
		t.Fatalf("composing start failed: stderr=%q", stderr)
	}
	_, stderr = runCmd(t, "--socket", fd.path(),
		"presence", "recording", "stop", "--chat", "5511900000000@s.whatsapp.net")
	if strings.Contains(stderr, "exec error") {
		t.Fatalf("recording stop failed: stderr=%q", stderr)
	}

	calls := fd.seen()
	if len(calls) != 2 ||
		calls[0].Method != "presence.composing.start" ||
		calls[1].Method != "presence.recording.stop" {
		t.Fatalf("expected composing.start then recording.stop, got %+v", calls)
	}
}

// TestWaPresenceMissingChat: --chat is required on every leaf.
func TestWaPresenceMissingChat(t *testing.T) {
	fd := newFakeDaemon(t)
	_, stderr := runCmd(t, "--socket", fd.path(), "presence", "composing", "start")
	if !strings.Contains(stderr, "--chat is required") {
		t.Fatalf("expected usage error, stderr=%q", stderr)
	}
	if n := len(fd.seen()); n != 0 {
		t.Fatalf("usage error must not emit RPCs, saw %d", n)
	}
}

// TestWaHistoryJSONStreamsNDJSON: `wa history --json` emits one
// schema-tagged line per message and forwards chat + default limit.
func TestWaHistoryJSONStreamsNDJSON(t *testing.T) {
	fd := newFakeDaemon(t)
	fd.on("history", func(params json.RawMessage) (any, *rpcError) {
		var p struct {
			Chat  string `json:"chat"`
			Limit int    `json:"limit"`
		}
		if err := json.Unmarshal(params, &p); err != nil {
			return nil, &rpcError{Code: -32602, Message: err.Error()}
		}
		if p.Chat != "5511900000000@s.whatsapp.net" {
			return nil, &rpcError{Code: -32602, Message: "wrong chat"}
		}
		if p.Limit != 50 {
			return nil, &rpcError{Code: -32602, Message: "default limit must be 50"}
		}
		return map[string]any{"messages": []map[string]any{
			{"timestamp": 1700000000, "senderJid": "x@s.whatsapp.net", "body": "oi", "isFromMe": false},
			{"timestamp": 1700000001, "senderJid": "me", "body": "olá", "isFromMe": true},
		}}, nil
	})

	stdout, stderr := runCmd(t, "--socket", fd.path(), "--json",
		"history", "--chat", "5511900000000@s.whatsapp.net")
	if strings.Contains(stderr, "exec error") {
		t.Fatalf("history failed: stderr=%q", stderr)
	}
	lines := strings.Split(strings.TrimSpace(stdout), "\n")
	if len(lines) != 2 {
		t.Fatalf("expected 2 NDJSON lines, got %d: %q", len(lines), stdout)
	}
	for _, ln := range lines {
		if !strings.Contains(ln, `"schema":"wa.history/v1"`) {
			t.Fatalf("line missing schema tag: %q", ln)
		}
	}
}

// TestWaSearchEmitsRPC: `wa search` forwards the FTS query + default
// limit and requires --query.
func TestWaSearchEmitsRPC(t *testing.T) {
	fd := newFakeDaemon(t)
	fd.on("search", func(params json.RawMessage) (any, *rpcError) {
		var p struct {
			Query string `json:"query"`
			Limit int    `json:"limit"`
		}
		if err := json.Unmarshal(params, &p); err != nil {
			return nil, &rpcError{Code: -32602, Message: err.Error()}
		}
		if p.Query != "boleto" || p.Limit != 20 {
			return nil, &rpcError{Code: -32602, Message: "wrong query/limit"}
		}
		return map[string]any{"messages": []map[string]any{}}, nil
	})

	_, stderr := runCmd(t, "--socket", fd.path(), "--json", "search", "--query", "boleto")
	if strings.Contains(stderr, "exec error") {
		t.Fatalf("search failed: stderr=%q", stderr)
	}
	calls := fd.seen()
	if len(calls) != 1 || calls[0].Method != "search" {
		t.Fatalf("expected 1 search call, got %+v", calls)
	}

	_, stderr = runCmd(t, "--socket", fd.path(), "search")
	if !strings.Contains(stderr, "--query is required") {
		t.Fatalf("expected usage error, stderr=%q", stderr)
	}
}

// TestWaThreadGetEmitsRPC: `wa thread get` forwards chat + empty
// cursor + default limit 50.
func TestWaThreadGetEmitsRPC(t *testing.T) {
	fd := newFakeDaemon(t)
	fd.on("thread.get", func(params json.RawMessage) (any, *rpcError) {
		var p struct {
			Chat   string `json:"chat"`
			Cursor string `json:"cursor"`
			Limit  int    `json:"limit"`
		}
		if err := json.Unmarshal(params, &p); err != nil {
			return nil, &rpcError{Code: -32602, Message: err.Error()}
		}
		if p.Chat != "5511900000000@s.whatsapp.net" || p.Cursor != "" || p.Limit != 50 {
			return nil, &rpcError{Code: -32602, Message: "wrong params"}
		}
		return map[string]any{"messages": []map[string]any{}, "nextCursor": ""}, nil
	})

	_, stderr := runCmd(t, "--socket", fd.path(),
		"thread", "get", "--chat", "5511900000000@s.whatsapp.net")
	if strings.Contains(stderr, "exec error") {
		t.Fatalf("thread get failed: stderr=%q", stderr)
	}
	calls := fd.seen()
	if len(calls) != 1 || calls[0].Method != "thread.get" {
		t.Fatalf("expected 1 thread.get call, got %+v", calls)
	}
}

// TestWaWaitForwardsEventsAndTimeout: `wa wait` splits --events on
// commas and converts --timeout to timeoutMs.
func TestWaWaitForwardsEventsAndTimeout(t *testing.T) {
	fd := newFakeDaemon(t)
	fd.on("wait", func(params json.RawMessage) (any, *rpcError) {
		var p struct {
			Events    []string `json:"events"`
			TimeoutMs int64    `json:"timeoutMs"`
		}
		if err := json.Unmarshal(params, &p); err != nil {
			return nil, &rpcError{Code: -32602, Message: err.Error()}
		}
		if len(p.Events) != 2 || p.Events[0] != "message" || p.Events[1] != "receipt" {
			return nil, &rpcError{Code: -32602, Message: "wrong events"}
		}
		if p.TimeoutMs != 5000 {
			return nil, &rpcError{Code: -32602, Message: "wrong timeoutMs"}
		}
		return map[string]any{"event": "message", "matched": true}, nil
	})

	_, stderr := runCmd(t, "--socket", fd.path(),
		"wait", "--events", "message,receipt", "--timeout", "5s")
	if strings.Contains(stderr, "exec error") {
		t.Fatalf("wait failed: stderr=%q", stderr)
	}
	calls := fd.seen()
	if len(calls) != 1 || calls[0].Method != "wait" {
		t.Fatalf("expected 1 wait call, got %+v", calls)
	}
}

// TestWaSessionLogoutAllEmitsRPC: `wa session logout-all` maps to
// session.logoutAll and forwards the idempotency key.
func TestWaSessionLogoutAllEmitsRPC(t *testing.T) {
	fd := newFakeDaemon(t)
	fd.on("session.logoutAll", func(params json.RawMessage) (any, *rpcError) {
		var p struct {
			IdempotencyKey string `json:"idempotencyKey"`
		}
		if err := json.Unmarshal(params, &p); err != nil {
			return nil, &rpcError{Code: -32602, Message: err.Error()}
		}
		if p.IdempotencyKey != "k-logout-1" {
			return nil, &rpcError{Code: -32602, Message: "idempotencyKey not forwarded"}
		}
		return map[string]any{}, nil
	})

	_, stderr := runCmd(t, "--socket", fd.path(),
		"session", "logout-all", "--idempotency-key", "k-logout-1")
	if strings.Contains(stderr, "exec error") {
		t.Fatalf("logout-all failed: stderr=%q", stderr)
	}
	calls := fd.seen()
	if len(calls) != 1 || calls[0].Method != "session.logoutAll" {
		t.Fatalf("expected 1 session.logoutAll call, got %+v", calls)
	}
}

// TestWaAllowAddRemoveList drives the three allow verbs end-to-end:
// add splits --actions, remove sends op=remove, list renders the
// default-deny hint when no rules exist.
func TestWaAllowAddRemoveList(t *testing.T) {
	fd := newFakeDaemon(t)
	fd.on("allow", func(params json.RawMessage) (any, *rpcError) {
		var p struct {
			Op      string   `json:"op"`
			JID     string   `json:"jid"`
			Actions []string `json:"actions"`
		}
		if err := json.Unmarshal(params, &p); err != nil {
			return nil, &rpcError{Code: -32602, Message: err.Error()}
		}
		switch p.Op {
		case "add":
			if p.JID != "5511900000000@s.whatsapp.net" {
				return nil, &rpcError{Code: -32602, Message: "wrong jid"}
			}
			if len(p.Actions) != 2 || p.Actions[0] != "send" || p.Actions[1] != "read" {
				return nil, &rpcError{Code: -32602, Message: "actions not split"}
			}
			return map[string]any{}, nil
		case "remove":
			if p.JID != "5511900000000@s.whatsapp.net" {
				return nil, &rpcError{Code: -32602, Message: "wrong jid"}
			}
			return map[string]any{}, nil
		case "list":
			return map[string]any{"rules": []map[string]any{}}, nil
		default:
			return nil, &rpcError{Code: -32602, Message: "unknown op " + p.Op}
		}
	})

	_, stderr := runCmd(t, "--socket", fd.path(),
		"allow", "add", "5511900000000@s.whatsapp.net", "--actions", "send,read")
	if strings.Contains(stderr, "exec error") {
		t.Fatalf("allow add failed: stderr=%q", stderr)
	}
	_, stderr = runCmd(t, "--socket", fd.path(),
		"allow", "remove", "5511900000000@s.whatsapp.net")
	if strings.Contains(stderr, "exec error") {
		t.Fatalf("allow remove failed: stderr=%q", stderr)
	}
	stdout, stderr := runCmd(t, "--socket", fd.path(), "allow", "list")
	if strings.Contains(stderr, "exec error") {
		t.Fatalf("allow list failed: stderr=%q", stderr)
	}
	if !strings.Contains(stdout, "No allowlist entries (default deny)") {
		t.Fatalf("expected default-deny hint, stdout=%q", stdout)
	}

	calls := fd.seen()
	if len(calls) != 3 {
		t.Fatalf("expected 3 allow calls, got %+v", calls)
	}

	// Missing --actions on add is a usage error before any RPC.
	_, stderr = runCmd(t, "--socket", fd.path(),
		"allow", "add", "5511900000000@s.whatsapp.net")
	if !strings.Contains(stderr, "--actions is required") {
		t.Fatalf("expected usage error, stderr=%q", stderr)
	}
}

// TestWaPanicEmitsRPC: `wa panic` is a single no-param RPC.
func TestWaPanicEmitsRPC(t *testing.T) {
	fd := newFakeDaemon(t)
	fd.on("panic", func(json.RawMessage) (any, *rpcError) {
		return map[string]any{"wiped": true}, nil
	})

	_, stderr := runCmd(t, "--socket", fd.path(), "panic")
	if strings.Contains(stderr, "exec error") {
		t.Fatalf("panic failed: stderr=%q", stderr)
	}
	calls := fd.seen()
	if len(calls) != 1 || calls[0].Method != "panic" {
		t.Fatalf("expected 1 panic call, got %+v", calls)
	}
}

// TestWaHealthHumanOutput: `wa health` renders the liveness probe
// fields; lastEventTs=0 renders as "-".
func TestWaHealthHumanOutput(t *testing.T) {
	fd := newFakeDaemon(t)
	fd.on("health", func(json.RawMessage) (any, *rpcError) {
		return map[string]any{"paired": true, "connected": false, "profile": "personal"}, nil
	})

	stdout, stderr := runCmd(t, "--socket", fd.path(), "health")
	if strings.Contains(stderr, "exec error") {
		t.Fatalf("health failed: stderr=%q", stderr)
	}
	for _, want := range []string{"profile:    personal", "paired:     true", "connected:  false", "last-event: -"} {
		if !strings.Contains(stdout, want) {
			t.Fatalf("health output missing %q:\n%s", want, stdout)
		}
	}
}

// TestWaSyncForceAndStatus: `wa sync force` forwards chat+count;
// `wa sync status` renders the engine state table.
func TestWaSyncForceAndStatus(t *testing.T) {
	fd := newFakeDaemon(t)
	fd.on("sync.force", func(params json.RawMessage) (any, *rpcError) {
		var p struct {
			Chat  string `json:"chat"`
			Count int    `json:"count"`
		}
		if err := json.Unmarshal(params, &p); err != nil {
			return nil, &rpcError{Code: -32602, Message: err.Error()}
		}
		if p.Chat != "5511900000000@s.whatsapp.net" || p.Count != 10 {
			return nil, &rpcError{Code: -32602, Message: "wrong params"}
		}
		return map[string]any{
			"requested": true, "connected": true, "chat": p.Chat,
			"count": 10, "received": 3, "delivered": true,
		}, nil
	})
	fd.on("sync.status", func(json.RawMessage) (any, *rpcError) {
		return map[string]any{
			"syncing": true, "inFlightForceReqs": 1,
			"queueDepth": 2, "queueCap": 64,
		}, nil
	})

	stdout, stderr := runCmd(t, "--socket", fd.path(),
		"sync", "force", "--chat", "5511900000000@s.whatsapp.net", "--count", "10")
	if strings.Contains(stderr, "exec error") {
		t.Fatalf("sync force failed: stderr=%q", stderr)
	}
	if !strings.Contains(stdout, "delivered — 3 message(s) synced") {
		t.Fatalf("sync force output missing delivered line:\n%s", stdout)
	}

	stdout, stderr = runCmd(t, "--socket", fd.path(), "sync", "status")
	if strings.Contains(stderr, "exec error") {
		t.Fatalf("sync status failed: stderr=%q", stderr)
	}
	for _, want := range []string{"syncing:        true", "in-flight:      1 force request(s)", "queue:          2 / 64 blobs"} {
		if !strings.Contains(stdout, want) {
			t.Fatalf("sync status output missing %q:\n%s", want, stdout)
		}
	}
}

// TestWaWebhookLifecycle drives add/list/rm/deliveries/replay against
// their webhook.* wire methods with default flag values.
func TestWaWebhookLifecycle(t *testing.T) {
	fd := newFakeDaemon(t)
	fd.on("webhook.add", func(params json.RawMessage) (any, *rpcError) {
		var p struct {
			URL    string `json:"url"`
			Topics string `json:"topics"`
		}
		if err := json.Unmarshal(params, &p); err != nil {
			return nil, &rpcError{Code: -32602, Message: err.Error()}
		}
		if p.URL != "https://example.invalid/hook" || p.Topics != "message" {
			return nil, &rpcError{Code: -32602, Message: "wrong url/topics"}
		}
		return map[string]any{"id": "ep-1", "secret": "whsec_x"}, nil
	})
	fd.on("webhook.list", func(json.RawMessage) (any, *rpcError) {
		return map[string]any{"endpoints": []map[string]any{}}, nil
	})
	fd.on("webhook.remove", func(params json.RawMessage) (any, *rpcError) {
		var p struct {
			ID string `json:"id"`
		}
		_ = json.Unmarshal(params, &p)
		if p.ID != "ep-1" {
			return nil, &rpcError{Code: -32602, Message: "wrong id"}
		}
		return map[string]any{}, nil
	})
	fd.on("webhook.deliveries", func(params json.RawMessage) (any, *rpcError) {
		var p struct {
			State string `json:"state"`
			Limit int    `json:"limit"`
		}
		_ = json.Unmarshal(params, &p)
		if p.State != "dead" || p.Limit != 50 {
			return nil, &rpcError{Code: -32602, Message: "wrong state/limit"}
		}
		return map[string]any{"deliveries": []map[string]any{}}, nil
	})
	fd.on("webhook.replay", func(params json.RawMessage) (any, *rpcError) {
		var p struct {
			ID string `json:"id"`
		}
		_ = json.Unmarshal(params, &p)
		if p.ID != "d-1" {
			return nil, &rpcError{Code: -32602, Message: "wrong id"}
		}
		return map[string]any{}, nil
	})

	steps := [][]string{
		{"webhook", "add", "https://example.invalid/hook"},
		{"webhook", "list"},
		{"webhook", "deliveries", "--state", "dead"},
		{"webhook", "replay", "d-1"},
		{"webhook", "rm", "ep-1"},
	}
	for _, args := range steps {
		_, stderr := runCmd(t, append([]string{"--socket", fd.path()}, args...)...)
		if strings.Contains(stderr, "exec error") {
			t.Fatalf("%v failed: stderr=%q", args, stderr)
		}
	}

	want := []string{"webhook.add", "webhook.list", "webhook.deliveries", "webhook.replay", "webhook.remove"}
	calls := fd.seen()
	if len(calls) != len(want) {
		t.Fatalf("expected %d calls, got %+v", len(want), calls)
	}
	for i, m := range want {
		if calls[i].Method != m {
			t.Fatalf("call %d: expected %s, got %s", i, m, calls[i].Method)
		}
	}
}

// TestWaSendMediaBySHA256 exercises the --sha256 staged-object path:
// no filesystem access, params carry sha256 and never path.
func TestWaSendMediaBySHA256(t *testing.T) {
	sha := strings.Repeat("ab", 32)
	fd := newFakeDaemon(t)
	fd.on("sendMedia", func(params json.RawMessage) (any, *rpcError) {
		var p map[string]any
		if err := json.Unmarshal(params, &p); err != nil {
			return nil, &rpcError{Code: -32602, Message: err.Error()}
		}
		if p["to"] != "5511900000000@s.whatsapp.net" || p["sha256"] != sha {
			return nil, &rpcError{Code: -32602, Message: "wrong to/sha256"}
		}
		if p["caption"] != "doc" {
			return nil, &rpcError{Code: -32602, Message: "caption not forwarded"}
		}
		if _, hasPath := p["path"]; hasPath {
			return nil, &rpcError{Code: -32602, Message: "path must be absent in sha256 mode"}
		}
		return map[string]any{"messageId": "m-media-1"}, nil
	})

	_, stderr := runCmd(
		t, "--socket", fd.path(),
		"sendMedia",
		"--to", "5511900000000@s.whatsapp.net",
		"--sha256", sha,
		"--caption", "doc",
	)
	if strings.Contains(stderr, "exec error") {
		t.Fatalf("sendMedia failed: stderr=%q", stderr)
	}
	calls := fd.seen()
	if len(calls) != 1 || calls[0].Method != "sendMedia" {
		t.Fatalf("expected 1 sendMedia call, got %+v", calls)
	}
}

// TestWaSendMediaUsageErrors: --to required; --path XOR --sha256.
func TestWaSendMediaUsageErrors(t *testing.T) {
	fd := newFakeDaemon(t)
	cases := []struct {
		args []string
		want string
	}{
		{[]string{"sendMedia", "--path", "/tmp/x.png"}, "--to is required"},
		{[]string{"sendMedia", "--to", "x@s.whatsapp.net"}, "one of --path or --sha256 is required"},
		{[]string{"sendMedia", "--to", "x@s.whatsapp.net", "--path", "/tmp/x.png", "--sha256", strings.Repeat("ab", 32)}, "mutually exclusive"},
		// The voice-note hints are gated on a DECLARED audio type, because
		// the daemon refuses a sniffed one. Catching it client-side turns a
		// bare -32602 into a reason the operator can act on.
		{[]string{"sendMedia", "--to", "x@s.whatsapp.net", "--path", "/tmp/x.ogg", "--ptt"}, "require an audio --mime"},
		{[]string{"sendMedia", "--to", "x@s.whatsapp.net", "--path", "/tmp/x.png", "--mime", "image/png", "--seconds", "7"}, "require an audio --mime"},
	}
	for _, tc := range cases {
		_, stderr := runCmd(t, append([]string{"--socket", fd.path()}, tc.args...)...)
		if !strings.Contains(stderr, tc.want) {
			t.Fatalf("%v: expected %q in stderr, got %q", tc.args, tc.want, stderr)
		}
	}
	if n := len(fd.seen()); n != 0 {
		t.Fatalf("usage errors must not emit RPCs, saw %d", n)
	}
}

// TestWaAuditVerifyEmptyLog: `wa audit verify` is filesystem-only (no
// RPC). An empty audit log with a valid sidecar key verifies as 0
// records.
func TestWaAuditVerifyEmptyLog(t *testing.T) {
	dir := t.TempDir()
	logPath := filepath.Join(dir, "audit.log")
	if err := os.WriteFile(logPath, nil, 0o600); err != nil {
		t.Fatalf("write log: %v", err)
	}
	keyPath := logPath + ".key"
	if err := os.WriteFile(keyPath, []byte(strings.Repeat("0a", 32)+"\n"), 0o600); err != nil {
		t.Fatalf("write key: %v", err)
	}

	stdout, stderr := runCmd(t, "audit", "verify", "--path", logPath)
	if strings.Contains(stderr, "exec error") {
		t.Fatalf("audit verify failed: stderr=%q", stderr)
	}
	if !strings.Contains(stdout, "ok: 0 records verified at "+logPath) {
		t.Fatalf("expected ok line, stdout=%q stderr=%q", stdout, stderr)
	}
}

// TestWaAuditVerifyMissingKeyHint: a missing sidecar key fails with
// the actionable bootstrap hint, not a bare ENOENT.
func TestWaAuditVerifyMissingKeyHint(t *testing.T) {
	dir := t.TempDir()
	logPath := filepath.Join(dir, "audit.log")
	if err := os.WriteFile(logPath, nil, 0o600); err != nil {
		t.Fatalf("write log: %v", err)
	}

	_, stderr := runCmd(t, "audit", "verify", "--path", logPath)
	if !strings.Contains(stderr, "pass `--key <path>`") {
		t.Fatalf("expected actionable key hint, stderr=%q", stderr)
	}
}

// TestWaSendMediaVoiceNoteParams: --ptt and --seconds reach the wire as the
// `ptt` / `seconds` RPC params. The fake daemon rejects the call unless both
// arrive, so a flag parsed into a variable nobody forwards fails here.
func TestWaSendMediaVoiceNoteParams(t *testing.T) {
	fd := newFakeDaemon(t)
	fd.on("sendMedia", func(params json.RawMessage) (any, *rpcError) {
		var p map[string]any
		if err := json.Unmarshal(params, &p); err != nil {
			return nil, &rpcError{Code: -32602, Message: err.Error()}
		}
		if p["ptt"] != true {
			return nil, &rpcError{Code: -32602, Message: "ptt not forwarded"}
		}
		if p["seconds"] != float64(7) {
			return nil, &rpcError{Code: -32602, Message: "seconds not forwarded"}
		}
		return map[string]any{"messageId": "m-ptt-1"}, nil
	})

	_, stderr := runCmd(
		t, "--socket", fd.path(),
		"sendMedia",
		"--to", "5511900000000@s.whatsapp.net",
		"--path", "/tmp/note.ogg",
		"--mime", "audio/ogg; codecs=opus",
		"--ptt", "--seconds", "7",
	)
	if strings.Contains(stderr, "exec error") {
		t.Fatalf("sendMedia --ptt failed: stderr=%q", stderr)
	}
}
