package mcpbridge

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// fakeCaller records forwarded daemon calls and returns canned results.
type fakeCaller struct {
	mu     sync.Mutex
	calls  []recordedCall
	result json.RawMessage
	rpcErr *RPCError
}

type recordedCall struct {
	method string
	params map[string]any
}

func (f *fakeCaller) call(_ context.Context, method string, params any) (json.RawMessage, *RPCError, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	rec := recordedCall{method: method}
	if params != nil {
		b, _ := json.Marshal(params)
		_ = json.Unmarshal(b, &rec.params)
	}
	f.calls = append(f.calls, rec)
	if f.rpcErr != nil {
		return nil, f.rpcErr, nil
	}
	res := f.result
	if res == nil {
		res = json.RawMessage(`{"ok":true}`)
	}
	return res, nil, nil
}

func (f *fakeCaller) last(t *testing.T) recordedCall {
	t.Helper()
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.calls) == 0 {
		t.Fatal("no daemon calls recorded")
	}
	return f.calls[len(f.calls)-1]
}

// session spins up the bridge server over in-memory transports and
// returns a connected client session.
func session(t *testing.T, fc *fakeCaller, cfg Config) *mcp.ClientSession {
	t.Helper()
	srv, err := NewServer(fc.call, cfg)
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	st, ct := mcp.NewInMemoryTransports()
	go func() { _ = srv.Run(context.Background(), st) }()
	client := mcp.NewClient(&mcp.Implementation{Name: "test-client", Version: "0"}, nil)
	cs, err := client.Connect(context.Background(), ct, nil)
	if err != nil {
		t.Fatalf("client connect: %v", err)
	}
	t.Cleanup(func() { _ = cs.Close() })
	return cs
}

func toolNames(t *testing.T, cs *mcp.ClientSession) map[string]bool {
	t.Helper()
	res, err := cs.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	names := make(map[string]bool, len(res.Tools))
	for _, tool := range res.Tools {
		names[tool.Name] = true
	}
	return names
}

// TestRegistration_Matrix pins which tools exist per (send-mode,
// read-only, toolsets) — registration IS the bridge's permission model.
func TestRegistration_Matrix(t *testing.T) {
	t.Parallel()

	t.Run("default: draft mode, all toolsets", func(t *testing.T) {
		t.Parallel()
		names := toolNames(t, session(t, &fakeCaller{}, Config{}))
		for _, want := range []string{
			"wa_send_message", "wa_send_media", "wa_schedule_message",
			"wa_search_messages", "wa_get_thread", "wa_wait_for_reply",
			"wa_transcribe_voice", "wa_resolve_contact", "wa_list_chats",
			"wa_draft_review",
		} {
			if !names[want] {
				t.Errorf("default config missing tool %s", want)
			}
		}
	})

	t.Run("deny: no send tools", func(t *testing.T) {
		t.Parallel()
		names := toolNames(t, session(t, &fakeCaller{}, Config{SendMode: SendModeDeny}))
		for _, banned := range []string{"wa_send_message", "wa_send_media", "wa_schedule_message"} {
			if names[banned] {
				t.Errorf("deny mode registered %s", banned)
			}
		}
		if !names["wa_get_thread"] || !names["wa_draft_review"] {
			t.Error("deny mode must keep read tools")
		}
	})

	t.Run("read-only beats direct", func(t *testing.T) {
		t.Parallel()
		names := toolNames(t, session(t, &fakeCaller{}, Config{SendMode: SendModeDirect, ReadOnly: true}))
		if names["wa_send_message"] {
			t.Error("read-only registered a send tool")
		}
	})

	t.Run("toolset filter", func(t *testing.T) {
		t.Parallel()
		names := toolNames(t, session(t, &fakeCaller{}, Config{Toolsets: []string{ToolsetContacts}}))
		if !names["wa_resolve_contact"] || !names["wa_list_chats"] {
			t.Error("contacts toolset missing its tools")
		}
		if names["wa_send_message"] || names["wa_draft_review"] {
			t.Error("toolset filter leaked tools from other sets")
		}
	})

	t.Run("invalid config rejected", func(t *testing.T) {
		t.Parallel()
		if _, err := NewServer((&fakeCaller{}).call, Config{SendMode: "yolo"}); err == nil {
			t.Error("invalid send-mode accepted")
		}
		if _, err := NewServer((&fakeCaller{}).call, Config{Toolsets: []string{"telepathy"}}); err == nil {
			t.Error("unknown toolset accepted")
		}
	})
}

// TestDraftGate proves the spec-111 default: wa_send_message in draft
// mode files a draft.create with origin=mcp and NEVER calls send.
func TestDraftGate(t *testing.T) {
	t.Parallel()
	fc := &fakeCaller{result: json.RawMessage(`{"draft":{"id":"drf-1","state":"pending_review"}}`)}
	cs := session(t, fc, Config{}) // default = draft

	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{
		Name:      "wa_send_message",
		Arguments: map[string]any{"to": "5511999999999", "body": "oi"},
	})
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	if res.IsError {
		t.Fatalf("tool errored: %+v", res.Content)
	}
	last := fc.last(t)
	if last.method != "draft.create" {
		t.Fatalf("draft mode forwarded %q, want draft.create", last.method)
	}
	if last.params["origin"] != "mcp" {
		t.Errorf("draft.create origin = %v, want mcp (FR-111-05)", last.params["origin"])
	}
	for _, c := range fc.calls {
		if c.method == "send" {
			t.Fatal("draft mode must never reach the send method")
		}
	}
}

// TestDirectMode pins the direct path: wa_send_message forwards "send".
func TestDirectMode(t *testing.T) {
	t.Parallel()
	fc := &fakeCaller{}
	cs := session(t, fc, Config{SendMode: SendModeDirect})
	if _, err := cs.CallTool(context.Background(), &mcp.CallToolParams{
		Name:      "wa_send_message",
		Arguments: map[string]any{"to": "5511999999999", "body": "oi"},
	}); err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	if got := fc.last(t).method; got != "send" {
		t.Fatalf("direct mode forwarded %q, want send", got)
	}
}

// TestPolicyRefusalIsInstructive proves SEP-1303: allowlist refusals
// become tool-execution errors carrying the operator remediation, not
// protocol errors.
func TestPolicyRefusalIsInstructive(t *testing.T) {
	t.Parallel()
	fc := &fakeCaller{rpcErr: &RPCError{Code: -32100, Message: "not allowlisted"}}
	cs := session(t, fc, Config{SendMode: SendModeDirect})
	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{
		Name:      "wa_send_message",
		Arguments: map[string]any{"to": "5511999999999", "body": "oi"},
	})
	if err != nil {
		t.Fatalf("policy refusal must be a tool error, got protocol error: %v", err)
	}
	if !res.IsError {
		t.Fatal("expected IsError on policy refusal")
	}
	var sb strings.Builder
	for _, c := range res.Content {
		if tc, ok := c.(*mcp.TextContent); ok {
			sb.WriteString(tc.Text)
		}
	}
	if !strings.Contains(sb.String(), "wa allow add") {
		t.Errorf("refusal text lacks remediation command: %q", sb.String())
	}
}

// TestDraftReviewIsReadOnly proves the agent cannot approve or reject:
// the only methods the safety tool ever forwards are draft.list/get.
func TestDraftReviewIsReadOnly(t *testing.T) {
	t.Parallel()
	fc := &fakeCaller{result: json.RawMessage(`{"drafts":[]}`)}
	cs := session(t, fc, Config{})
	if _, err := cs.CallTool(context.Background(), &mcp.CallToolParams{
		Name: "wa_draft_review", Arguments: map[string]any{},
	}); err != nil {
		t.Fatalf("CallTool list: %v", err)
	}
	if _, err := cs.CallTool(context.Background(), &mcp.CallToolParams{
		Name: "wa_draft_review", Arguments: map[string]any{"draft_id": "drf-1"},
	}); err != nil {
		t.Fatalf("CallTool get: %v", err)
	}
	for _, c := range fc.calls {
		if c.method != "draft.list" && c.method != "draft.get" {
			t.Fatalf("draft review forwarded %q — must stay read-only", c.method)
		}
	}
}

// TestScheduleValidation pins bridge-side input checks: bad RFC3339 and
// past timestamps fail BEFORE any daemon call.
func TestScheduleValidation(t *testing.T) {
	t.Parallel()
	fc := &fakeCaller{}
	cs := session(t, fc, Config{SendMode: SendModeDirect})
	for _, sendAt := range []string{"tomorrow 9am", "2020-01-01T00:00:00Z"} {
		res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{
			Name:      "wa_schedule_message",
			Arguments: map[string]any{"to": "5511999999999", "body": "oi", "send_at": sendAt},
		})
		if err != nil {
			t.Fatalf("CallTool(%q): %v", sendAt, err)
		}
		if !res.IsError {
			t.Errorf("send_at=%q accepted, want validation error", sendAt)
		}
	}
	fc.mu.Lock()
	n := len(fc.calls)
	fc.mu.Unlock()
	if n != 0 {
		t.Errorf("invalid schedule input reached the daemon (%d calls)", n)
	}
}
