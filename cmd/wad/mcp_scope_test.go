package main

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/yolo-labz/wa/v2/internal/adapters/primary/rest"
)

// recordingDispatcher answers every method with {} (media methods with
// one row, object and chunk, so the media tools reach the daemon) and
// records the names.
type recordingDispatcher struct {
	mu      sync.Mutex
	methods []string
}

func (d *recordingDispatcher) Handle(_ context.Context, method string, _ json.RawMessage) (json.RawMessage, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.methods = append(d.methods, method)
	canned := map[string]string{
		"media.list":       `{"media":[{"messageId":"3EB0X","chatJid":"5511900000001"}]}`,
		"media.download":   `{"object":{"sha256":"ab","size":1}}`,
		"media.fetchBytes": `{"bytes":"AQ==","eof":true}`,
	}
	if res, ok := canned[method]; ok {
		return json.RawMessage(res), nil
	}
	return json.RawMessage(`{}`), nil
}

func (d *recordingDispatcher) take() []string {
	d.mu.Lock()
	defer d.mu.Unlock()
	out := d.methods
	d.methods = nil
	return out
}

var scopeLadder = []rest.Scope{rest.ScopeReadStr, rest.ScopeSendStr, rest.ScopeAdminStr}

// sampleArgs fills every required argument of tool with a valid value,
// plus sender (wa_list_media's narrowing filter); handle is a live
// media_handle for wa_get_media.
func sampleArgs(tool *mcp.Tool, handle string) map[string]any {
	values := map[string]any{
		"to": "5511900000001", "chat": "5511900000001", "recipient": "5511900000001", "body": "hi",
		"path": "/tmp/x.jpg", "query": "q", "message_id": "3EB0X", "action": "send",
		"send_at": time.Now().Add(time.Hour).Format(time.RFC3339), "media_handle": handle,
		"job_id": "j1",
	}
	args := map[string]any{}
	if tool.Name == "wa_list_media" {
		args["sender"] = "5511900000002"
	}
	schema, _ := tool.InputSchema.(map[string]any)
	required, _ := schema["required"].([]any)
	for _, k := range required {
		args[k.(string)] = values[k.(string)]
	}
	return args
}

// toolMethods calls every tool listed at scope and returns the daemon
// methods each one reached, plus each tool's declared required scope.
func toolMethods(t *testing.T, scope rest.Scope) (methods map[string][]string, declared map[string]string) {
	t.Helper()
	d := &recordingDispatcher{}
	cs := mcpHTTPSession(t, d, scope)
	listed, err := cs.ListTools(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	methods, declared = map[string][]string{}, map[string]string{}
	handle := ""
	// wa_list_media first: its handle feeds wa_get_media.
	slices.SortFunc(listed.Tools, func(a, b *mcp.Tool) int {
		return strings.Compare(strings.Replace(a.Name, "wa_list_media", "", 1), strings.Replace(b.Name, "wa_list_media", "", 1))
	})
	for _, tool := range listed.Tools {
		declared[tool.Name], _ = tool.Meta["required_scope"].(string)
		res, err := cs.CallTool(t.Context(), &mcp.CallToolParams{Name: tool.Name, Arguments: sampleArgs(tool, handle)})
		if err != nil {
			t.Fatalf("%s at %s: %v", tool.Name, scope, err)
		}
		if tool.Name == "wa_list_media" {
			raw, _ := json.Marshal(res.StructuredContent)
			var out struct {
				Media []struct {
					Handle string `json:"media_handle"`
				} `json:"media"`
			}
			if json.Unmarshal(raw, &out) != nil || len(out.Media) != 1 {
				t.Fatalf("wa_list_media at %s: %s", scope, raw)
			}
			handle = out.Media[0].Handle
		}
		methods[tool.Name] = d.take()
	}
	return methods, declared
}

func rank(s string) int { return slices.Index(scopeLadder, rest.Scope(s)) }

// TestMCPScope_ToolListIsFunctionOfScope is FR-116-11: at each scope the
// listed tools are exactly those whose required scope it covers, and each
// tool's declared scope is the highest rest.RequiredScope of what it calls.
func TestMCPScope_ToolListIsFunctionOfScope(t *testing.T) {
	t.Setenv("WAD_MCP_SEND_MODE", "direct")
	_, all := toolMethods(t, rest.ScopeAdminStr)
	for _, scope := range scopeLadder {
		methods, declared := toolMethods(t, scope)
		for name, req := range all {
			if _, listed := declared[name]; listed != (rank(req) <= rank(string(scope))) {
				t.Errorf("scope %s: %s (requires %s) listed=%v", scope, name, req, listed)
			}
		}
		for name, ms := range methods {
			highest := -1
			for _, m := range ms {
				req, ok := rest.RequiredScope(m, nil)
				if !ok {
					t.Errorf("%s calls %s, which has no rest scope", name, m)
				}
				highest = max(highest, rank(req.String()))
			}
			if highest != rank(declared[name]) {
				t.Errorf("%s declares %q but calls %v", name, declared[name], ms)
			}
		}
	}
}

// TestMCPScope_NoAdminMutationTools is SR-116-09: no tool, even at admin
// scope, reaches an allowlist, pairing, session or token mutation.
func TestMCPScope_NoAdminMutationTools(t *testing.T) {
	t.Setenv("WAD_MCP_SEND_MODE", "direct")
	methods, _ := toolMethods(t, rest.ScopeAdminStr)
	for name, ms := range methods {
		for _, m := range ms {
			mutation := m == "allow" || m == "pair" || m == "panic" ||
				strings.HasPrefix(m, "session.") || strings.HasPrefix(m, "token") || strings.HasPrefix(m, "admin.")
			if mutation {
				t.Errorf("tool %s reaches admin mutation %s", name, m)
			}
		}
	}
}

// TestMCPScope_UnlistedToolIsUnknown: calling a tool above the caller's
// scope is "unknown tool", never a scope refusal, and reaches nothing.
func TestMCPScope_UnlistedToolIsUnknown(t *testing.T) {
	t.Setenv("WAD_MCP_SEND_MODE", "direct")
	for scope, tools := range map[rest.Scope][]string{
		rest.ScopeReadStr: {"wa_send_message", "wa_wait_for_reply", "wa_schedule_message"},
		rest.ScopeSendStr: {"wa_schedule_message"},
	} {
		d := &recordingDispatcher{}
		cs := mcpHTTPSession(t, d, scope)
		for _, name := range tools {
			_, err := cs.CallTool(t.Context(), &mcp.CallToolParams{Name: name, Arguments: map[string]any{}})
			if err == nil || !strings.Contains(err.Error(), "unknown tool") || strings.Contains(err.Error(), "scope") {
				t.Errorf("%s at %s: err %v, want unknown tool", name, scope, err)
			}
		}
		if got := d.take(); len(got) != 0 {
			t.Errorf("unlisted tools at %s reached the daemon: %v", scope, got)
		}
	}
}
