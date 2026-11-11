package evals

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/yolo-labz/wa/v2/internal/mcpbridge"
)

// evalSession connects an in-process client to a bridge built with cfg.
func evalSession(t *testing.T, call mcpbridge.Caller, cfg mcpbridge.Config) *mcp.ClientSession {
	t.Helper()
	server, err := mcpbridge.NewServer(call, cfg)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	srv, cli := mcp.NewInMemoryTransports()
	done := make(chan struct{})
	go func() { defer close(done); _ = server.Run(ctx, srv) }()
	cs, err := mcp.NewClient(&mcp.Implementation{Name: "eval", Version: "1"}, nil).Connect(ctx, cli, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cs.Close(); cancel(); <-done })
	return cs
}

func structured(t *testing.T, res *mcp.CallToolResult, out any) {
	t.Helper()
	raw, _ := json.Marshal(res.StructuredContent)
	if err := json.Unmarshal(raw, out); err != nil {
		t.Fatalf("structured content %s: %v", raw, err)
	}
}

// evalPreflightBeforeSend is F2: a read-scope agent can ask "may I send to
// X?" before trying, by name or number, and learns only that one answer.
func evalPreflightBeforeSend(t *testing.T) {
	var checked map[string]any
	call := mcpbridge.Caller(func(_ context.Context, method string, params any) (json.RawMessage, *mcpbridge.RPCError, error) {
		switch method {
		case "contacts.search":
			if params.(map[string]any)["query"] == "Alice" {
				return json.RawMessage(`{"contacts":[{"jid":"15550001"}]}`), nil, nil
			}
			return json.RawMessage(`{"contacts":[{"jid":"15550001"},{"jid":"15550002"}]}`), nil, nil
		case "allow.check":
			checked = params.(map[string]any)
			return json.RawMessage(`{"allowed":false,"action":"send","remediation":"ask the operator"}`), nil, nil
		}
		t.Errorf("unexpected daemon call %s", method)
		return json.RawMessage(`{}`), nil, nil
	})
	cs := evalSession(t, call, mcpbridge.Config{Scope: mcpbridge.ScopeRead})
	listed, err := cs.ListTools(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	var tool *mcp.Tool
	for _, lt := range listed.Tools {
		if lt.Name == "wa_allowlist_check" {
			tool = lt
		}
	}
	if tool == nil || !descCovers(tool.Description, "before") || !descCovers(tool.Description, "wa_send_message") {
		t.Fatalf("read scope must list a pre-flight that says to run before wa_send_message: %+v", tool)
	}
	res, err := cs.CallTool(t.Context(), &mcp.CallToolParams{
		Name: "wa_allowlist_check", Arguments: map[string]any{"recipient": "Alice", "action": "send"},
	})
	if err != nil || res.IsError {
		t.Fatalf("pre-flight failed: %v %+v", err, res)
	}
	var got map[string]any
	structured(t, res, &got)
	if checked["jid"] != "15550001" || checked["action"] != "send" || got["allowed"] != false || len(got) != 3 {
		t.Fatalf("checked %v, answered %v; want one resolved (jid, action) and only {allowed, action, remediation}", checked, got)
	}
	res, err = cs.CallTool(t.Context(), &mcp.CallToolParams{
		Name: "wa_allowlist_check", Arguments: map[string]any{"recipient": "Al", "action": "send"},
	})
	if err != nil || !res.IsError || !strings.Contains(res.Content[0].(*mcp.TextContent).Text, "wa_resolve_contact") {
		t.Fatalf("ambiguous name must point at wa_resolve_contact: %v %+v", err, res)
	}
}

// evalScopeShownBeforeCall is F3: every tool states its scope up front, and
// a read-scope agent learns from wa_status that sending needs send.
func evalScopeShownBeforeCall(t *testing.T) {
	for name, tool := range serverTools(t, mcpbridge.ScopeAdmin) {
		scope, _ := tool.Meta["required_scope"].(string)
		if scope == "" || !strings.Contains(tool.Description, "Requires scope: "+scope+".") {
			t.Errorf("%s: _meta.required_scope %q not stated in its description", name, scope)
		}
	}
	call := mcpbridge.Caller(func(context.Context, string, any) (json.RawMessage, *mcpbridge.RPCError, error) {
		return json.RawMessage(`{"connected":true}`), nil, nil
	})
	cs := evalSession(t, call, mcpbridge.Config{Scope: mcpbridge.ScopeRead})
	res, err := cs.CallTool(t.Context(), &mcp.CallToolParams{
		Name: "wa_status", Arguments: map[string]any{"tool": "wa_send_message"},
	})
	if err != nil || res.IsError {
		t.Fatalf("wa_status: %v %+v", err, res)
	}
	var got struct {
		Connected bool `json:"connected"`
		Auth      struct {
			Scope         string `json:"scope"`
			RequiredScope string `json:"required_scope"`
		} `json:"auth"`
	}
	structured(t, res, &got)
	if !got.Connected || got.Auth.Scope != "read" || got.Auth.RequiredScope != "send" {
		t.Fatalf("wa_status = %+v, want scope read and wa_send_message requiring send", got)
	}
}
