package main

import (
	"context"
	"encoding/json"
	"github.com/yolo-labz/wa/v2/internal/adapters/primary/rest"
	"log/slog"
	"testing"
)

type draftScopeSpy struct{ methods []string }

func (s *draftScopeSpy) Handle(_ context.Context, method string, _ json.RawMessage) (json.RawMessage, error) {
	s.methods = append(s.methods, method)
	return json.RawMessage(`{"draft":{"id":"synthetic"}}`), nil
}

func TestDraftScopeRuntimeRefusal(t *testing.T) {
	spy := &draftScopeSpy{}
	call := dispatcherCaller(spy, rest.ScopeDraftStr)
	for _, method := range []string{"send", "sendMedia", "react", "draft.approve", "draft.reject", "schedule.send", "schedule.cancel", "media.upload", "pair", "session.logout", "group.create", "allow", "admin.reload", "future.method"} {
		_, rpcErr, err := call(context.Background(), method, map[string]any{"to": "123@s.whatsapp.net", "body": "x"})
		if err != nil || rpcErr == nil || rpcErr.Code != -32099 {
			t.Errorf("%s: %+v %v", method, rpcErr, err)
		}
	}
	for _, args := range []map[string]any{
		{"to": "123@s.whatsapp.net", "body": "x", "path": "/tmp/file"},
		{"to": "123@s.whatsapp.net", "body": "x", "caption": "media"},
		{"to": "123@s.whatsapp.net", "body": "x", "idempotencyKey": "unsupported"},
		{"to": "123@s.whatsapp.net", "body": true},
	} {
		_, rpcErr, err := call(context.Background(), "draft.create", args)
		if err != nil || rpcErr == nil {
			t.Errorf("invalid params allowed: %+v %v", args, err)
		}
	}
	if len(spy.methods) != 0 {
		t.Fatalf("denied calls dispatched: %v", spy.methods)
	}
	for _, tc := range []struct {
		method string
		params map[string]any
	}{
		{"status", nil},
		{"draft.create", map[string]any{"to": "123@s.whatsapp.net", "body": "pending review", "origin": "mcp"}},
	} {
		_, rpcErr, err := call(context.Background(), tc.method, tc.params)
		if rpcErr != nil || err != nil {
			t.Errorf("allowed %s: %+v %v", tc.method, rpcErr, err)
		}
	}
	if len(spy.methods) != 2 || spy.methods[0] != "status" || spy.methods[1] != "draft.create" {
		t.Fatalf("unexpected dispatches: %v", spy.methods)
	}
}

func TestDraftScopeProviderEverySendMode(t *testing.T) {
	for _, mode := range []string{"direct", "draft", "deny"} {
		t.Run(mode, func(t *testing.T) {
			t.Setenv("WAD_MCP_SEND_MODE", mode)
			provider, err := buildMCPProvider(&draftScopeSpy{}, "test", slog.New(slog.DiscardHandler))
			if err != nil {
				t.Fatal(err)
			}
			if provider(rest.ScopeDraftStr) == nil {
				t.Fatal("draft handler not registered")
			}
			if provider(rest.ScopeReadStr) == nil || provider(rest.ScopeSendStr) == nil || provider(rest.ScopeAdminStr) == nil {
				t.Fatal("legacy handler missing")
			}
			if provider(rest.Scope("unknown")) != nil {
				t.Fatal("unknown handler")
			}
		})
	}
}
func TestDraftScopeLegacyRuntimePermissions(t *testing.T) {
	for _, tc := range []struct {
		scope  rest.Scope
		method string
		want   bool
	}{
		{rest.ScopeReadStr, "status", true},
		{rest.ScopeReadStr, "send", false},
		{rest.ScopeSendStr, "draft.approve", true},
		{rest.ScopeSendStr, "send", true},
		{rest.ScopeSendStr, "pair", false},
		{rest.ScopeAdminStr, "pair", true},
	} {
		spy := &draftScopeSpy{}
		_, rpcErr, err := dispatcherCaller(spy, tc.scope)(context.Background(), tc.method, nil)
		if err != nil || ((rpcErr == nil) != tc.want) {
			t.Errorf("%s/%s err=%v refusal=%v", tc.scope, tc.method, err, rpcErr)
		}
		if (len(spy.methods) > 0) != tc.want {
			t.Errorf("%s/%s dispatch mismatch", tc.scope, tc.method)
		}
	}
}
