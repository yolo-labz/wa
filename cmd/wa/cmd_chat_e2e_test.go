// Package main — end-to-end tests for feature 018 T2-08 `wa chat`
// subcommand tree. Matches tasks-tier2.md T2-08 gate test names:
//   - TestWaChatArchive
//   - TestWaChatMuteFuture
//   - TestWaChatPin
package main

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

// TestWaChatArchive exercises `wa chat archive --chat ...` — verifies one
// chat.archive RPC with archived=true.
func TestWaChatArchive(t *testing.T) {
	fd := newFakeDaemon(t)
	fd.on("chat.archive", func(params json.RawMessage) (any, *rpcError) {
		var p struct {
			Chat     string `json:"chat"`
			Archived bool   `json:"archived"`
		}
		if err := json.Unmarshal(params, &p); err != nil {
			return nil, &rpcError{Code: -32602, Message: err.Error()}
		}
		if p.Chat != "5511900000000@s.whatsapp.net" {
			return nil, &rpcError{Code: -32602, Message: "wrong chat"}
		}
		if !p.Archived {
			return nil, &rpcError{Code: -32602, Message: "archived must be true"}
		}
		return map[string]any{}, nil
	})

	stdout, stderr := runCmd(t,
		"--socket", fd.path(),
		"--json",
		"chat", "archive",
		"--chat", "5511900000000@s.whatsapp.net",
	)
	_ = stdout
	if strings.Contains(stderr, "exec error") {
		t.Fatalf("archive failed:\nstdout=%q\nstderr=%q", stdout, stderr)
	}

	calls := fd.seen()
	if len(calls) != 1 || calls[0].Method != "chat.archive" {
		t.Fatalf("expected 1 chat.archive call, got %+v", calls)
	}
}

// TestWaChatMuteFuture exercises `wa chat mute --chat ... --duration 1h` —
// verifies one chat.mute RPC with a future untilUnix value.
func TestWaChatMuteFuture(t *testing.T) {
	fd := newFakeDaemon(t)
	fd.on("chat.mute", func(params json.RawMessage) (any, *rpcError) {
		var p struct {
			Chat      string `json:"chat"`
			UntilUnix int64  `json:"untilUnix"`
		}
		if err := json.Unmarshal(params, &p); err != nil {
			return nil, &rpcError{Code: -32602, Message: err.Error()}
		}
		if p.Chat == "" {
			return nil, &rpcError{Code: -32602, Message: "chat required"}
		}
		if p.UntilUnix <= time.Now().Unix() {
			return nil, &rpcError{Code: -32100, Message: "policy_refused: untilUnix must be in the future"}
		}
		return map[string]any{}, nil
	})

	stdout, stderr := runCmd(t,
		"--socket", fd.path(),
		"--json",
		"chat", "mute",
		"--chat", "5511900000000@s.whatsapp.net",
		"--duration", "1h",
	)
	_ = stdout
	if strings.Contains(stderr, "exec error") {
		t.Fatalf("mute future failed:\nstdout=%q\nstderr=%q", stdout, stderr)
	}

	calls := fd.seen()
	if len(calls) != 1 || calls[0].Method != "chat.mute" {
		t.Fatalf("expected 1 chat.mute call, got %+v", calls)
	}
}

// TestWaChatPin exercises `wa chat pin --chat ...` — verifies one
// chat.pin RPC with pinned=true.
func TestWaChatPin(t *testing.T) {
	fd := newFakeDaemon(t)
	fd.on("chat.pin", func(params json.RawMessage) (any, *rpcError) {
		var p struct {
			Chat   string `json:"chat"`
			Pinned bool   `json:"pinned"`
		}
		if err := json.Unmarshal(params, &p); err != nil {
			return nil, &rpcError{Code: -32602, Message: err.Error()}
		}
		if !p.Pinned {
			return nil, &rpcError{Code: -32602, Message: "pinned must be true"}
		}
		return map[string]any{}, nil
	})

	stdout, stderr := runCmd(t,
		"--socket", fd.path(),
		"--json",
		"chat", "pin",
		"--chat", "5511900000000@s.whatsapp.net",
	)
	_ = stdout
	if strings.Contains(stderr, "exec error") {
		t.Fatalf("pin failed:\nstdout=%q\nstderr=%q", stdout, stderr)
	}

	calls := fd.seen()
	if len(calls) != 1 || calls[0].Method != "chat.pin" {
		t.Fatalf("expected 1 chat.pin call, got %+v", calls)
	}
}
