package evals

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/yolo-labz/wa/v2/internal/adapters/secondary/memory"
	"github.com/yolo-labz/wa/v2/internal/app"
	"github.com/yolo-labz/wa/v2/internal/domain"
	"github.com/yolo-labz/wa/v2/internal/mcpbridge"
)

// evalMissingMessage is F1: an agent asking for media of a message id the
// daemon never stored must learn "no such message", not "the recipient is
// not allowlisted" (the old -32199..-32100 band read -32117 that way). The
// real wa_transcribe_voice tool runs against the real dispatcher and media
// store; errors cross the bridge the way /mcp sends them, via app.RPCWire.
func evalMissingMessage(t *testing.T) {
	t.Helper()
	m := memory.New(nil)
	media, err := memory.NewMediaStore(t.TempDir(), nil)
	if err != nil {
		t.Fatal(err)
	}
	d := app.NewDispatcher(app.DispatcherConfig{Session: m, Events: m, Media: media})
	defer func() { _ = d.Close() }()
	caller := mcpbridge.Caller(func(ctx context.Context, method string, params any) (json.RawMessage, *mcpbridge.RPCError, error) {
		raw, err := json.Marshal(params)
		if err != nil {
			return nil, nil, err
		}
		res, err := d.Handle(ctx, method, raw)
		if err != nil {
			code, msg, reason := app.RPCWire(err)
			data, _ := json.Marshal(domain.ErrorData{Reason: reason})
			return nil, &mcpbridge.RPCError{Code: int(code), Message: msg, Data: data}, nil
		}
		return res, nil, nil
	})
	server, err := mcpbridge.NewServer(caller, mcpbridge.Config{})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	srv, cli := mcp.NewInMemoryTransports()
	done := make(chan struct{})
	go func() { defer close(done); _ = server.Run(ctx, srv) }()
	client, err := mcp.NewClient(&mcp.Implementation{Name: "f1-eval", Version: "1"}, nil).Connect(ctx, cli, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = client.Close(); cancel(); <-done }()

	result, err := client.CallTool(ctx, &mcp.CallToolParams{
		Name: "wa_transcribe_voice", Arguments: map[string]any{"message_id": "3EB0NEVERSTORED"},
	})
	if err != nil {
		t.Fatalf("protocol error, want a tool error: %v", err)
	}
	raw, _ := json.Marshal(result.StructuredContent)
	var te mcpbridge.ToolError
	if err := json.Unmarshal(raw, &te); err != nil || !result.IsError {
		t.Fatalf("IsError=%v structured=%s: %v", result.IsError, raw, err)
	}
	text := result.Content[0].(*mcp.TextContent).Text
	if te.Code != -32117 || te.Reason != "message_not_found" || !strings.Contains(te.Remediation, "wa_get_thread") {
		t.Fatalf("missing message reached the agent as %+v (%q)", te, text)
	}
	if strings.Contains(strings.ToLower(text), "allowlist") {
		t.Fatalf("missing message blamed on the allowlist: %q", text)
	}
}
