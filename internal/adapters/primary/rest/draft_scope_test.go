package rest

import (
	"encoding/json"
	"fmt"
	"net/http"
	"testing"
)

func TestDraftScopeExplicitMethodGate(t *testing.T) {
	valid := json.RawMessage(`{"to":"123@s.whatsapp.net","body":"review this"}`)
	for _, m := range []string{"status", "thread.get", "messages.search", "draft.get", "draft.list", "allow.check", "auth.whoami"} {
		if !AllowedScope(m, nil, ScopeDraft) {
			t.Errorf("draft should inherit read: %s", m)
		}
	}
	if !AllowedScope("draft.create", valid, ScopeDraft) {
		t.Fatal("text proposal denied")
	}
	for _, m := range []string{"send", "sendMedia", "react", "markRead", "send.reply", "media.upload", "draft.approve", "draft.reject", "schedule.send", "schedule.cancel", "pair", "allow", "group.create", "session.logout", "future.unknown", "message.revoke"} {
		if AllowedScope(m, valid, ScopeDraft) {
			t.Errorf("draft unexpectedly authorizes %s", m)
		}
	}
	for _, raw := range []string{
		`{}`, `null`, `[]`, `{"to":"","body":"x"}`, `{"to":"123@s.whatsapp.net","body":" "}`,
		`{"to":"123@s.whatsapp.net","body":3}`,
		`{"to":"123@s.whatsapp.net","body":"x","path":"/tmp/example"}`,
		`{"to":"123@s.whatsapp.net","body":"x","path":""}`,
		`{"to":"123@s.whatsapp.net","body":"x","caption":"caption"}`,
		`{"to":"123@s.whatsapp.net","body":"x","schedule":1}`,
		`{"to":"123@s.whatsapp.net","body":"x","idempotencyKey":"fake"}`,
		`{"to":"123@s.whatsapp.net","body":"x","origin":"operator"}`,
		`{"to":"123@s.whatsapp.net","body":"x"} {}`,
	} {
		if AllowedScope("draft.create", json.RawMessage(raw), ScopeDraft) {
			t.Errorf("bad draft accepted: %s", raw)
		}
	}
	if !AllowedScope("draft.create", json.RawMessage(`{"to":"123@s.whatsapp.net","body":"x","origin":"mcp"}`), ScopeDraft) {
		t.Fatal("MCP origin denied")
	}
	for _, s := range []MethodScope{ScopeRead, ScopeSend, ScopeAdmin} {
		if !AllowedScope("status", nil, s) {
			t.Errorf("legacy %v lost read", s)
		}
	}
	if AllowedScope("draft.create", valid, ScopeRead) {
		t.Fatal("read gained proposal")
	}
	if !AllowedScope("draft.create", json.RawMessage(`{"path":"/tmp/legacy"}`), ScopeSend) ||
		!AllowedScope("draft.create", json.RawMessage(`{"path":"/tmp/legacy"}`), ScopeAdmin) {
		t.Fatal("legacy media draft changed")
	}
	if !AllowedScope("send", nil, ScopeSend) || !AllowedScope("pair", nil, ScopeAdmin) {
		t.Fatal("legacy scope regressed")
	}
}

func TestDraftScopeRESTGate(t *testing.T) {
	store := newFakeStore()
	store.Add("draft-token", ScopeDraftStr)
	srv := newScopedServerForTest(t, store)
	rows := []struct {
		method, params string
		status         int
	}{
		{"status", "{}", http.StatusOK},
		{"draft.create", `{"to":"123@s.whatsapp.net","body":"synthetic proposal"}`, http.StatusOK},
		{"draft.create", `{"to":"123@s.whatsapp.net","body":"synthetic proposal","path":"/tmp/media"}`, http.StatusForbidden},
		{"draft.create", `{"to":"123@s.whatsapp.net","body":"synthetic proposal","unknown":1}`, http.StatusForbidden},
		{"send", `{"to":"123@s.whatsapp.net","body":"x"}`, http.StatusForbidden},
		{"sendMedia", "{}", http.StatusForbidden},
		{"media.upload", "{}", http.StatusForbidden},
		{"draft.approve", "{}", http.StatusForbidden},
		{"draft.reject", "{}", http.StatusForbidden},
		{"schedule.send", "{}", http.StatusForbidden},
		{"allow", "{}", http.StatusForbidden},
	}
	for i, tc := range rows {
		t.Run(fmt.Sprintf("%d-%s", i, tc.method), func(t *testing.T) {
			raw := fmt.Sprintf(`{"jsonrpc":"2.0","id":1,"method":%q,"params":%s}`, tc.method, tc.params)
			resp := postScoped(t, srv.ListenerAddr().String(), "draft-token", raw)
			if resp.StatusCode != tc.status {
				t.Errorf("%s HTTP=%d want %d", tc.method, resp.StatusCode, tc.status)
			}
		})
	}
}
