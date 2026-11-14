package mcpbridge

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/yolo-labz/wa/v2/internal/domain"
)

// typedRefusal asserts a tool result is an error carrying a structured
// {code, reason, remediation} and returns its text.
func typedRefusal(t *testing.T, name string, res *mcp.CallToolResult, out map[string]any, want domain.ErrorReason) string {
	t.Helper()
	if !res.IsError {
		t.Fatalf("%s: IsError=false, want a refusal", name)
	}
	if out["reason"] != string(want) || out["remediation"] == "" || out["code"] == nil {
		t.Fatalf("%s: structured=%v, want reason %q with code and remediation", name, out, want)
	}
	return res.Content[0].(*mcp.TextContent).Text
}

// TestRemediate_LocalRefusalsAreTyped pins WA-MCP-SEC F4: refusals the
// bridge makes itself (bad send_at, ambiguous recipient, daemon down) reach
// the agent typed like daemon refusals, and the daemon-down text carries
// no socket path or dial error.
func TestRemediate_LocalRefusalsAreTyped(t *testing.T) {
	t.Parallel()
	cs := session(t, &fakeCaller{result: json.RawMessage(`{"contacts":[]}`)}, Config{SendMode: SendModeDirect, Scope: ScopeAdmin})
	for name, args := range map[string]map[string]any{
		"send_at not RFC 3339": {"to": "+5511999999999", "body": "hi", "send_at": "tomorrow"},
		"send_at in the past":  {"to": "+5511999999999", "body": "hi", "send_at": "2001-01-01T00:00:00Z"},
	} {
		res, out := callTool(t, cs, "wa_schedule_message", args)
		if text := typedRefusal(t, name, res, out, domain.ReasonInvalidParams); strings.Contains(text, "tomorrow") {
			t.Errorf("%s: text echoes the raw input: %q", name, text)
		}
	}
	res, out := callTool(t, cs, "wa_allowlist_check", map[string]any{"recipient": "Maria", "action": "send"})
	if text := typedRefusal(t, "ambiguous recipient", res, out, domain.ReasonInvalidParams); strings.Contains(text, "Maria") {
		t.Errorf("ambiguous recipient: text echoes the name: %q", text)
	}

	down := func(context.Context, string, any) (json.RawMessage, *RPCError, error) {
		return nil, nil, errors.New("dial unix /run/user/1000/wa/wa.sock: connect: no such file or directory")
	}
	srv, err := NewServer(down, Config{})
	if err != nil {
		t.Fatal(err)
	}
	st, ct := mcp.NewInMemoryTransports()
	go func() { _ = srv.Run(t.Context(), st) }()
	dcs, err := mcp.NewClient(&mcp.Implementation{Name: "t", Version: "1"}, nil).Connect(t.Context(), ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = dcs.Close() })
	res, out = callTool(t, dcs, "wa_status", nil)
	if text := typedRefusal(t, "daemon down", res, out, domain.ReasonDisconnected); strings.Contains(text, "/run/") || strings.Contains(text, "sock") {
		t.Errorf("daemon down: text leaks the dial error: %q", text)
	}
}

// TestRemediate_TextHasNoDaemonDetail pins WA-MCP-SEC F8: a daemon
// refusal's message (RPCWire's whole error chain) never reaches the agent,
// except for invalid_params, whose message names the caller's own field.
func TestRemediate_TextHasNoDaemonDetail(t *testing.T) {
	t.Parallel()
	const leak = "upstream: 5511988887777@s.whatsapp.net in /var/lib/wa/messages.db"
	for _, r := range domain.ErrorReasons() {
		text := toolErr(t, &RPCError{Code: -32000, Message: leak, Data: reasonData(t, domain.ErrorData{Reason: r})}).Error()
		if got := strings.Contains(text, leak); got != (r == domain.ReasonInvalidParams) {
			t.Errorf("reason %s: daemon message in text = %v: %q", r, got, text)
		}
	}
}

// TestTranscribeVoice_MalformedResultIsTyped pins p8's F4 L1: a malformed
// daemon answer to wa_transcribe_voice is the typed errMalformedResult, not
// a wrapped parser error that could carry daemon text.
func TestTranscribeVoice_MalformedResultIsTyped(t *testing.T) {
	t.Parallel()
	h := mintMediaHandle("p", "chat", "id", time.Now().Add(time.Minute))
	call := func(context.Context, string, any) (json.RawMessage, *RPCError, error) {
		return json.RawMessage(`<html>/var/lib/wa/secret.db`), nil, nil
	}
	if _, err := transcribeVoice(t.Context(), call, "p", h); !errors.Is(err, errMalformedResult) {
		t.Fatalf("err = %v, want errMalformedResult", err)
	}
}
