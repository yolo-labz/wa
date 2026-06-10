package main

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"testing"

	"github.com/yolo-labz/wa/v2/internal/adapters/primary/rest"
	"github.com/yolo-labz/wa/v2/internal/mcpbridge"
)

type codedErr struct{ code int }

func (e *codedErr) Error() string { return "boom" }
func (e *codedErr) RPCCode() int  { return e.code }

type stubRESTDispatcher struct {
	result json.RawMessage
	err    error
}

func (s *stubRESTDispatcher) Handle(context.Context, string, json.RawMessage) (json.RawMessage, error) {
	return s.result, s.err
}

// TestDispatcherCaller_ErrorMapping pins the in-process error contract:
// typed dispatcher errors keep their RPCCode (generic message — REST
// leak policy), untyped errors collapse to -32603, success passes raw.
func TestDispatcherCaller_ErrorMapping(t *testing.T) {
	t.Parallel()

	call := dispatcherCaller(&stubRESTDispatcher{err: &codedErr{code: -32100}})
	_, rpcErr, err := call(context.Background(), "send", map[string]any{"to": "x"})
	if err != nil || rpcErr == nil || rpcErr.Code != -32100 || rpcErr.Message != "rpc error" {
		t.Fatalf("typed error: rpcErr=%+v err=%v", rpcErr, err)
	}

	call = dispatcherCaller(&stubRESTDispatcher{err: errors.New("plain")})
	_, rpcErr, err = call(context.Background(), "send", nil)
	if err != nil || rpcErr == nil || rpcErr.Code != -32603 || rpcErr.Message != "internal error" {
		t.Fatalf("untyped error: rpcErr=%+v err=%v", rpcErr, err)
	}

	call = dispatcherCaller(&stubRESTDispatcher{result: json.RawMessage(`{"ok":true}`)})
	raw, rpcErr, err := call(context.Background(), "status", nil)
	if err != nil || rpcErr != nil || string(raw) != `{"ok":true}` {
		t.Fatalf("success: raw=%s rpcErr=%+v err=%v", raw, rpcErr, err)
	}
}

func TestParseMCPSendMode(t *testing.T) {
	t.Parallel()
	log := slog.New(slog.DiscardHandler)
	cases := map[string]string{
		"":        mcpbridge.SendModeDraft,
		"draft":   mcpbridge.SendModeDraft,
		"direct":  mcpbridge.SendModeDirect,
		"deny":    mcpbridge.SendModeDeny,
		"garbage": mcpbridge.SendModeDraft, // fail closed to draft
	}
	for raw, want := range cases {
		if got := parseMCPSendMode(raw, log); got != want {
			t.Errorf("parseMCPSendMode(%q) = %q, want %q", raw, got, want)
		}
	}
}

// TestBuildMCPProvider_ScopeClasses pins FR-111-02 fail-closed routing:
// read/send/admin resolve to handlers, anything else returns nil.
func TestBuildMCPProvider_ScopeClasses(t *testing.T) {
	log := slog.New(slog.DiscardHandler)
	provider, err := buildMCPProvider(&stubRESTDispatcher{result: json.RawMessage(`{}`)}, "test", log)
	if err != nil {
		t.Fatalf("buildMCPProvider: %v", err)
	}
	if provider == nil {
		t.Fatal("provider nil without WAD_MCP_DISABLE")
	}
	if provider(rest.ScopeReadStr) == nil {
		t.Error("read scope: nil handler")
	}
	if provider(rest.ScopeSendStr) == nil || provider(rest.ScopeAdminStr) == nil {
		t.Error("send/admin scope: nil handler")
	}
	if provider(rest.Scope("weird")) != nil {
		t.Error("unknown scope must fail closed (nil)")
	}
	if provider(rest.ScopeReadStr) == provider(rest.ScopeSendStr) {
		t.Error("read and send scopes must be DIFFERENT handlers (registration filtering)")
	}
}

func TestMCPDisabled(t *testing.T) {
	t.Setenv("WAD_MCP_DISABLE", "1")
	if !mcpDisabled() {
		t.Fatal("WAD_MCP_DISABLE=1 not honoured")
	}
	t.Setenv("WAD_MCP_DISABLE", "")
	if mcpDisabled() {
		t.Fatal("empty WAD_MCP_DISABLE must not disable")
	}
}
