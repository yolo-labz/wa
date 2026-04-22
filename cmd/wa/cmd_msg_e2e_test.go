// Package main — end-to-end tests for feature 018 T2-06 `wa msg`
// subcommand tree. Matches tasks-tier2.md T2-06 gate test names:
//   - TestWaMsgRevokeScopeSelf
//   - TestWaMsgEditWithinWindow
//   - TestWaMsgEditRefusedOutsideWindow
package main

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestWaMsgRevokeScopeSelf exercises `wa msg revoke --chat … --messageId …
// --scope self`. Verifies the CLI emits exactly one message.revoke RPC with
// scope=self and the daemon's empty result is surfaced cleanly.
func TestWaMsgRevokeScopeSelf(t *testing.T) {
	fd := newFakeDaemon(t)
	fd.on("message.revoke", func(params json.RawMessage) (any, *rpcError) {
		var p struct {
			Chat      string `json:"chat"`
			MessageID string `json:"messageId"`
			Scope     string `json:"scope"`
		}
		if err := json.Unmarshal(params, &p); err != nil {
			return nil, &rpcError{Code: -32602, Message: err.Error()}
		}
		if p.Chat != "5511900000000@s.whatsapp.net" {
			return nil, &rpcError{Code: -32602, Message: "wrong chat"}
		}
		if p.MessageID != "orig-99" {
			return nil, &rpcError{Code: -32602, Message: "wrong messageId"}
		}
		if p.Scope != "self" {
			return nil, &rpcError{Code: -32602, Message: "scope must be self, got " + p.Scope}
		}
		return map[string]any{}, nil
	})

	stdout, stderr := runCmd(t,
		"--socket", fd.path(),
		"--json",
		"msg", "revoke",
		"--chat", "5511900000000@s.whatsapp.net",
		"--messageId", "orig-99",
		"--scope", "self",
	)
	_ = stdout
	if strings.Contains(stderr, "exec error") {
		t.Fatalf("revoke self failed:\nstdout=%q\nstderr=%q", stdout, stderr)
	}

	calls := fd.seen()
	if len(calls) != 1 || calls[0].Method != "message.revoke" {
		t.Fatalf("expected 1 message.revoke call, got %+v", calls)
	}
}

// TestWaMsgEditWithinWindow exercises the happy path: `wa msg edit` emits
// one message.edit RPC; daemon returns empty result; exit code 0.
func TestWaMsgEditWithinWindow(t *testing.T) {
	fd := newFakeDaemon(t)
	fd.on("message.edit", func(params json.RawMessage) (any, *rpcError) {
		var p struct {
			Chat      string `json:"chat"`
			MessageID string `json:"messageId"`
			Body      string `json:"body"`
		}
		if err := json.Unmarshal(params, &p); err != nil {
			return nil, &rpcError{Code: -32602, Message: err.Error()}
		}
		if p.Chat == "" || p.MessageID == "" || p.Body == "" {
			return nil, &rpcError{Code: -32602, Message: "chat/messageId/body required"}
		}
		return map[string]any{}, nil
	})

	stdout, stderr := runCmd(t,
		"--socket", fd.path(),
		"--json",
		"msg", "edit",
		"--chat", "5511900000000@s.whatsapp.net",
		"--messageId", "orig-42",
		"--body", "typo fixed",
		"--idempotency-key", "edit-1",
	)
	_ = stdout
	if strings.Contains(stderr, "exec error") {
		t.Fatalf("edit within window failed:\nstdout=%q\nstderr=%q", stdout, stderr)
	}

	calls := fd.seen()
	if len(calls) != 1 || calls[0].Method != "message.edit" {
		t.Fatalf("expected 1 message.edit call, got %+v", calls)
	}
}

// TestWaMsgEditRefusedOutsideWindow verifies the wire error path: daemon
// returns -32100 policy_refused (the code `ErrOutsideEditWindow` maps to)
// and the CLI surfaces it as a non-zero exit (exec error visible in stderr).
func TestWaMsgEditRefusedOutsideWindow(t *testing.T) {
	fd := newFakeDaemon(t)
	fd.on("message.edit", func(_ json.RawMessage) (any, *rpcError) {
		return nil, &rpcError{
			Code:    -32100,
			Message: "policy_refused: outside edit window",
		}
	})

	_, stderr := runCmd(t,
		"--socket", fd.path(),
		"--json",
		"msg", "edit",
		"--chat", "5511900000000@s.whatsapp.net",
		"--messageId", "ancient-1",
		"--body", "too late",
	)

	if !strings.Contains(stderr, "policy_refused") {
		t.Fatalf("expected policy_refused in stderr, got: %q", stderr)
	}
	if !strings.Contains(stderr, "exec error") {
		t.Fatalf("expected non-zero exit (exec error marker) in stderr: %q", stderr)
	}

	calls := fd.seen()
	if len(calls) != 1 || calls[0].Method != "message.edit" {
		t.Fatalf("expected 1 message.edit call, got %+v", calls)
	}
}
