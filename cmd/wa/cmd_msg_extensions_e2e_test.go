// Package main — end-to-end tests for feature 018 T2-23 `wa msg` extension
// subcommands (forward / star / disappearing).
package main

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestWaMsgReplyForwardStar exercises the three new subcommands end-to-end
// against a fake daemon. Matches the T2-23 gate test name in tasks-tier2.md.
// Covers `wa msg forward`, `wa msg star` (both --star and --unstar), and
// `wa msg disappearing` with a preset and a raw integer.
func TestWaMsgReplyForwardStar(t *testing.T) {
	fd := newFakeDaemon(t)

	fd.on("message.forward", func(params json.RawMessage) (any, *rpcError) {
		var p struct {
			To         string `json:"to"`
			SourceChat string `json:"sourceChat"`
			MessageID  string `json:"messageId"`
		}
		if err := json.Unmarshal(params, &p); err != nil {
			return nil, &rpcError{Code: -32602, Message: err.Error()}
		}
		if p.To == "" || p.SourceChat == "" || p.MessageID == "" {
			return nil, &rpcError{Code: -32602, Message: "to/sourceChat/messageId required"}
		}
		return map[string]any{"messageId": "fwd-1", "timestamp": 1_700_000_000}, nil
	})

	fd.on("message.star", func(params json.RawMessage) (any, *rpcError) {
		var p struct {
			Chat      string `json:"chat"`
			MessageID string `json:"messageId"`
			Starred   bool   `json:"starred"`
		}
		if err := json.Unmarshal(params, &p); err != nil {
			return nil, &rpcError{Code: -32602, Message: err.Error()}
		}
		if p.Chat == "" || p.MessageID == "" {
			return nil, &rpcError{Code: -32602, Message: "chat/messageId required"}
		}
		return map[string]any{}, nil
	})

	fd.on("message.setDisappearing", func(params json.RawMessage) (any, *rpcError) {
		var p struct {
			Chat    string `json:"chat"`
			Seconds int    `json:"seconds"`
		}
		if err := json.Unmarshal(params, &p); err != nil {
			return nil, &rpcError{Code: -32602, Message: err.Error()}
		}
		if p.Chat == "" {
			return nil, &rpcError{Code: -32602, Message: "chat required"}
		}
		switch p.Seconds {
		case 0, 86_400, 604_800, 7_776_000:
		default:
			return nil, &rpcError{Code: -32602, Message: "seconds must be in {0,86400,604800,7776000}"}
		}
		return map[string]any{}, nil
	})

	// Forward
	_, stderr := runCmd(t,
		"--socket", fd.path(),
		"--json",
		"msg", "forward",
		"--to", "5511900000000@s.whatsapp.net",
		"--sourceChat", "5511888888888@s.whatsapp.net",
		"--messageId", "orig-1",
	)
	if strings.Contains(stderr, "exec error") {
		t.Fatalf("forward failed: %q", stderr)
	}

	// Star on
	_, stderr = runCmd(t,
		"--socket", fd.path(),
		"--json",
		"msg", "star",
		"--chat", "5511900000000@s.whatsapp.net",
		"--messageId", "m-1",
	)
	if strings.Contains(stderr, "exec error") {
		t.Fatalf("star on failed: %q", stderr)
	}

	// Star off (--unstar)
	_, stderr = runCmd(t,
		"--socket", fd.path(),
		"--json",
		"msg", "star",
		"--chat", "5511900000000@s.whatsapp.net",
		"--messageId", "m-1",
		"--unstar",
	)
	if strings.Contains(stderr, "exec error") {
		t.Fatalf("star off failed: %q", stderr)
	}

	// Disappearing via preset
	_, stderr = runCmd(t,
		"--socket", fd.path(),
		"--json",
		"msg", "disappearing",
		"--chat", "5511900000000@s.whatsapp.net",
		"--seconds", "7d",
	)
	if strings.Contains(stderr, "exec error") {
		t.Fatalf("disappearing preset failed: %q", stderr)
	}

	// Disappearing via raw integer (off)
	_, stderr = runCmd(t,
		"--socket", fd.path(),
		"--json",
		"msg", "disappearing",
		"--chat", "5511900000000@s.whatsapp.net",
		"--seconds", "0",
	)
	if strings.Contains(stderr, "exec error") {
		t.Fatalf("disappearing raw int failed: %q", stderr)
	}

	calls := fd.seen()
	if len(calls) != 5 {
		t.Fatalf("expected 5 RPC calls, got %d: %+v", len(calls), calls)
	}
	wantMethods := []string{
		"message.forward",
		"message.star",
		"message.star",
		"message.setDisappearing",
		"message.setDisappearing",
	}
	for i, c := range calls {
		if c.Method != wantMethods[i] {
			t.Errorf("call[%d] method = %q, want %q", i, c.Method, wantMethods[i])
		}
	}

	// Verify --unstar flipped the boolean.
	var starOn, starOff struct {
		Starred bool `json:"starred"`
	}
	if err := json.Unmarshal(calls[1].Params, &starOn); err != nil {
		t.Fatalf("decode star on: %v", err)
	}
	if err := json.Unmarshal(calls[2].Params, &starOff); err != nil {
		t.Fatalf("decode star off: %v", err)
	}
	if !starOn.Starred {
		t.Error("first star call: starred=false, want true")
	}
	if starOff.Starred {
		t.Error("--unstar call: starred=true, want false")
	}

	// Verify preset → seconds translation.
	var disPreset, disRaw struct {
		Seconds int `json:"seconds"`
	}
	if err := json.Unmarshal(calls[3].Params, &disPreset); err != nil {
		t.Fatalf("decode preset: %v", err)
	}
	if err := json.Unmarshal(calls[4].Params, &disRaw); err != nil {
		t.Fatalf("decode raw: %v", err)
	}
	if disPreset.Seconds != 604_800 {
		t.Errorf("preset 7d → %d, want 604800", disPreset.Seconds)
	}
	if disRaw.Seconds != 0 {
		t.Errorf("raw 0 → %d, want 0", disRaw.Seconds)
	}
}

// TestWaMsgDisappearingRejectsBadPreset verifies the CLI rejects an unknown
// preset / non-integer string locally with exit=64 (usage error) before any
// RPC is dispatched.
func TestWaMsgDisappearingRejectsBadPreset(t *testing.T) {
	fd := newFakeDaemon(t)

	_, stderr := runCmd(t,
		"--socket", fd.path(),
		"msg", "disappearing",
		"--chat", "5511900000000@s.whatsapp.net",
		"--seconds", "bogus",
	)
	if !strings.Contains(stderr, "off|24h|7d|90d") {
		t.Errorf("expected preset help in stderr, got: %q", stderr)
	}
	if n := len(fd.seen()); n != 0 {
		t.Errorf("expected 0 RPC calls on bad preset, got %d", n)
	}
}
