package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"testing"

	"github.com/yolo-labz/wa/v2/internal/adapters/primary/rest"
	"github.com/yolo-labz/wa/v2/internal/app"
	"github.com/yolo-labz/wa/v2/internal/domain"
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

// TestDispatcherCaller_UsesRPCWire pins that /mcp translates dispatcher
// errors through app.RPCWire, the table socket and REST use: same code,
// same leak-safe message, error.data.reason on every row. The private
// RPCCode-or--32603 mapping it replaces sent plain sentinels out as
// -32603 and kept not-allowlisted's own -32012 (F1, F2).
func TestDispatcherCaller_UsesRPCWire(t *testing.T) {
	t.Parallel()
	rows := append(app.RPCWireErrors(), errors.New("plain"), &codedErr{code: -32100})
	for _, row := range rows {
		err := fmt.Errorf("handler: %w", row)
		_, rpcErr, callErr := dispatcherCaller(&stubRESTDispatcher{err: err})(context.Background(), "send", map[string]any{"to": "x"})
		code, msg, reason := app.RPCWire(err)
		if callErr != nil || rpcErr == nil {
			t.Fatalf("%v: rpcErr=%+v err=%v", row, rpcErr, callErr)
		}
		var data domain.ErrorData
		if jErr := json.Unmarshal(rpcErr.Data, &data); jErr != nil {
			t.Fatalf("%v: data %s: %v", row, rpcErr.Data, jErr)
		}
		if rpcErr.Code != int(code) || rpcErr.Message != msg || data.Reason != reason || data.Reason == "" {
			t.Errorf("%v: got %+v reason %q, want (%d, %q, %q)", row, rpcErr, data.Reason, code, msg, reason)
		}
	}

	for _, tc := range []struct {
		err    error
		code   int
		reason domain.ErrorReason
	}{
		{domain.ErrMediaNotCached, -32301, domain.ReasonMediaNotCached},
		{app.ErrNotAllowlisted, -32100, domain.ReasonNotAllowlisted},
		{app.ErrMessageNotFound, -32117, domain.ReasonMessageNotFound},
		{errors.New("plain"), -32603, domain.ReasonInternal},
	} {
		_, rpcErr, _ := dispatcherCaller(&stubRESTDispatcher{err: tc.err})(context.Background(), "send", nil)
		if rpcErr == nil || rpcErr.Code != tc.code || !strings.Contains(string(rpcErr.Data), string(tc.reason)) {
			t.Errorf("%v: rpcErr=%+v, want %d %s", tc.err, rpcErr, tc.code, tc.reason)
		}
	}

	call := dispatcherCaller(&stubRESTDispatcher{result: json.RawMessage(`{"ok":true}`)})
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
