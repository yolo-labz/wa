package mcpbridge

import (
	"context"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/yolo-labz/wa/v2/internal/agentdocs"
)

// toolInvocation is one tool call with arguments chosen to exercise
// every optional branch, so a param the bridge only sets sometimes
// still gets checked.
type toolInvocation struct {
	tool string
	args map[string]any
}

var bridgeInvocations = []toolInvocation{
	{"wa_send_message", map[string]any{"to": "5511999999999", "body": "oi"}},
	{"wa_send_media", map[string]any{"to": "5511999999999", "path": "/tmp/a.jpg", "caption": "c"}},
	{"wa_schedule_message", map[string]any{"to": "5511999999999", "body": "oi", "send_at": "2099-01-01T09:00:00Z"}},
	{"wa_search_messages", map[string]any{"query": "boleto", "chat": "5511999999999@s.whatsapp.net", "limit": 5}},
	{"wa_get_thread", map[string]any{"chat": "5511999999999@s.whatsapp.net", "cursor": "c1", "limit": 5}},
	{"wa_wait_for_reply", map[string]any{"timeout_sec": 5}},
	{"wa_transcribe_voice", map[string]any{"message_id": "3EB0"}},
	{"wa_resolve_contact", map[string]any{"query": "pedro", "limit": 3}},
	{"wa_list_chats", map[string]any{"limit": 5}},
	{"wa_draft_review", map[string]any{}},
	{"wa_draft_review", map[string]any{"draft_id": "drf-1"}},
	{"wa_group_info", map[string]any{}},
	{"wa_group_info", map[string]any{"jid": "120363@g.us"}},
	{"wa_status", map[string]any{}},
}

// TestForwardedParamsAreDocumented drives every registered tool and
// asserts each forwarded parameter is one the target method documents.
//
// This is the guard that catches a tool aimed at the wrong method.
// wa_search_messages forwarded {phrase, chat, limit} to the legacy
// "search", which requires "query" and has no chat filter — every call
// failed with -32602 and no test noticed, because the older tests only
// assert which tools are LISTED and which method a send tool picks.
// Comparing against the published catalog checks the shape too.
func TestForwardedParamsAreDocumented(t *testing.T) {
	t.Parallel()

	documented, err := agentdocs.ParamsByMethod()
	if err != nil {
		t.Fatalf("read catalog: %v", err)
	}

	// Both send modes: draft mode reroutes the send tools to
	// draft.create, so direct-only coverage would leave that shape
	// unchecked (and vice versa).
	for _, cfg := range []Config{{}, {SendMode: SendModeDirect}} {
		fc := &fakeCaller{}
		cs := session(t, fc, cfg)
		for _, inv := range bridgeInvocations {
			res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{
				Name: inv.tool, Arguments: inv.args,
			})
			if err != nil {
				t.Fatalf("%s: CallTool: %v", inv.tool, err)
			}
			if res.IsError {
				t.Fatalf("%s: tool errored: %+v", inv.tool, res.Content)
			}
		}

		fc.mu.Lock()
		calls := append([]recordedCall(nil), fc.calls...)
		fc.mu.Unlock()

		if len(calls) < len(bridgeInvocations) {
			t.Fatalf("send-mode %q: %d calls recorded for %d invocations",
				cfg.SendMode, len(calls), len(bridgeInvocations))
		}
		for _, c := range calls {
			params, ok := documented[c.method]
			if !ok {
				t.Errorf("bridge forwards %q, which openrpc.json does not document — publish it or point the tool at a documented method", c.method)
				continue
			}
			allowed := make(map[string]bool, len(params))
			for _, p := range params {
				allowed[p.Name] = true
			}
			for name := range c.params {
				if !allowed[name] {
					t.Errorf("bridge sends %q parameter %q, which %q does not accept", c.method, name, c.method)
				}
			}
			for _, p := range params {
				if p.Required && c.params[p.Name] == nil {
					t.Errorf("bridge calls %q without its required parameter %q", c.method, p.Name)
				}
			}
		}
	}
}
