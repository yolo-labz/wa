package mcpbridge

import (
	"context"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestDraftScopeProposalToolsEverySendMode(t *testing.T) {
	for _, mode := range []string{SendModeDirect, SendModeDraft, SendModeDeny} {
		t.Run(mode, func(t *testing.T) {
			fc := &fakeCaller{}
			cs := session(t, fc, Config{Scope: ScopeDraft, SendMode: mode})
			names := toolNames(t, cs)
			for _, name := range []string{"wa_status", "wa_get_thread", "wa_draft_review", "wa_create_message_draft"} {
				if !names[name] {
					t.Errorf("draft missing %s in %s", name, mode)
				}
			}
			for _, name := range []string{"wa_send_message", "wa_send_media", "wa_schedule_message", "wa_wait_for_reply", "wa_sync_history"} {
				if names[name] {
					t.Errorf("draft exposes %s in %s", name, mode)
				}
				if _, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: name, Arguments: map[string]any{}}); err == nil {
					t.Errorf("draft called unregistered %s in %s", name, mode)
				}
			}
			res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{
				Name: "wa_create_message_draft", Arguments: map[string]any{"to": "123@s.whatsapp.net", "body": "synthetic human review"},
			})
			if err != nil || res == nil || res.IsError {
				t.Fatalf("proposal: res=%+v err=%v", res, err)
			}
			rec := fc.last(t)
			if rec.method != "draft.create" || rec.params["to"] != "123@s.whatsapp.net" ||
				rec.params["body"] != "synthetic human review" || rec.params["origin"] != "mcp" ||
				rec.params["path"] != nil || rec.params["caption"] != nil {
				t.Errorf("unexpected call: %+v", rec)
			}
			fc.mu.Lock()
			n := len(fc.calls)
			fc.mu.Unlock()
			if n != 1 {
				t.Fatalf("additional/transmitting RPCs: %d", n)
			}
		})
	}
}

func TestDraftScopeRejectsToolUnexpectedParameters(t *testing.T) {
	for _, args := range []map[string]any{
		{"to": "123@s.whatsapp.net", "body": "x", "path": "/tmp/example"},
		{"to": "123@s.whatsapp.net", "body": "x", "caption": "media"},
		{"to": "123@s.whatsapp.net", "body": "x", "idempotency_key": "false-promise"},
		{"to": "123@s.whatsapp.net", "body": 4},
		{"to": "123@s.whatsapp.net", "body": ""},
		{"to": "", "body": "test"},
	} {
		fc := &fakeCaller{}
		cs := session(t, fc, Config{Scope: ScopeDraft, SendMode: SendModeDirect})
		res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: "wa_create_message_draft", Arguments: args})
		if err == nil && res != nil && !res.IsError {
			t.Errorf("unexpected arguments accepted: %#v", args)
		}
		fc.mu.Lock()
		n := len(fc.calls)
		fc.mu.Unlock()
		if n != 0 {
			t.Errorf("invalid arguments forwarded: %d calls", n)
		}
	}
}

func TestDraftScopeLegacyMCPRegistration(t *testing.T) {
	for _, scope := range []string{ScopeRead, ScopeSend, ScopeAdmin} {
		names := toolNames(t, session(t, &fakeCaller{}, Config{Scope: scope, SendMode: SendModeDirect}))
		if names["wa_create_message_draft"] {
			t.Errorf("legacy %s gained draft-only tool", scope)
		}
		if !names["wa_status"] {
			t.Errorf("legacy %s lost read", scope)
		}
		if names["wa_send_message"] != (scope != ScopeRead) {
			t.Errorf("legacy %s send changed", scope)
		}
	}
}
