package evals

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/yolo-labz/wa/v2/internal/adapters/secondary/memory"
	"github.com/yolo-labz/wa/v2/internal/app"
	"github.com/yolo-labz/wa/v2/internal/domain"
	"github.com/yolo-labz/wa/v2/internal/mcpbridge"
)

type statusProbe bool

func (p statusProbe) WebsocketConnected() bool { return bool(p) }

// Run the real wa_status tool against the real dispatcher, not a canned RPC
// result. Only session/connectivity/storage inputs are fake and process-local.
func evalStatusHealth(t *testing.T, paired, connected bool, storage, wantHealth, wantReason string) {
	t.Helper()
	m := memory.New(nil)
	session, err := domain.NewSession(domain.MustJID("15551234567@s.whatsapp.net"), 1, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if paired {
		if err = m.Save(t.Context(), session); err != nil {
			t.Fatal(err)
		}
	}
	d := app.NewDispatcher(app.DispatcherConfig{Session: m, Events: m, Websocket: statusProbe(connected), StorageHealth: func() domain.StorageHealth { return domain.StorageHealth{Storage: storage, FreeBytes: 128 << 20} }})
	defer func() { _ = d.Close() }()
	payload := callStatus(t, d)
	var value struct {
		Connected bool     `json:"connected"`
		Health    string   `json:"health"`
		Reasons   []string `json:"reasons"`
		JID       string   `json:"jid"`
	}
	if err = json.Unmarshal([]byte(payload), &value); err != nil {
		t.Fatal(err)
	}
	wantJID := ""
	if paired {
		wantJID = "***4567"
	}
	if value.Connected != (paired && connected) || value.Health != wantHealth || len(value.Reasons) == 0 || value.Reasons[0] != wantReason || value.JID != wantJID || d.IsReady(t.Context()) {
		t.Fatalf("lying status: %s", payload)
	}
	if strings.Contains(payload, "15551234567") {
		t.Fatal("status leaked account JID")
	}
}

// callStatus runs the real wa_status tool over in-memory MCP transports
// against d and returns its text payload.
func callStatus(t *testing.T, d *app.Dispatcher) string {
	t.Helper()
	caller := mcpbridge.Caller(func(ctx context.Context, method string, _ any) (json.RawMessage, *mcpbridge.RPCError, error) {
		raw, err := d.Handle(ctx, method, nil)
		return raw, nil, err
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
	client, err := mcp.NewClient(&mcp.Implementation{Name: "status-eval", Version: "1"}, nil).Connect(ctx, cli, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = client.Close(); cancel(); <-done }()
	result, err := client.CallTool(ctx, &mcp.CallToolParams{Name: "wa_status", Arguments: map[string]any{}})
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError || len(result.Content) != 1 {
		t.Fatalf("status tool result=%+v", result)
	}
	text, ok := result.Content[0].(*mcp.TextContent)
	if !ok {
		t.Fatalf("non-text result=%T", result.Content[0])
	}
	return text.Text
}
