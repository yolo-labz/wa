package evals

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/yolo-labz/wa/v2/internal/mcpbridge"
)

// evalMediaListNarrowing is FR-116-15 from the agent's side: the tool
// says up front that chat plus sender or caption is required, a chat-only
// list is refused with that instruction, and a narrowed one goes through.
func evalMediaListNarrowing(t *testing.T) {
	var reached []string
	call := mcpbridge.Caller(func(_ context.Context, method string, _ any) (json.RawMessage, *mcpbridge.RPCError, error) {
		reached = append(reached, method)
		return json.RawMessage(`{"media":[],"appliedFilters":["chat","sender"]}`), nil, nil
	})
	tool := serverTools(t, mcpbridge.ScopeRead)["wa_list_media"]
	for _, must := range []string{"chat", "sender", "caption", "required", "wa_get_media"} {
		if tool == nil || !descCovers(tool.Description, must) {
			t.Fatalf("wa_list_media description must cover %q: %+v", must, tool)
		}
	}
	cs := evalSession(t, call, mcpbridge.Config{Scope: mcpbridge.ScopeRead})
	res, err := cs.CallTool(t.Context(), &mcp.CallToolParams{
		Name: "wa_list_media", Arguments: map[string]any{"chat": "5511900000001", "media_type": "image"},
	})
	if err != nil || !res.IsError || !strings.Contains(res.Content[0].(*mcp.TextContent).Text, "sender or a caption") {
		t.Fatalf("chat-only list: %v %+v", err, res)
	}
	res, err = cs.CallTool(t.Context(), &mcp.CallToolParams{
		Name: "wa_list_media", Arguments: map[string]any{"chat": "5511900000001", "sender": "5511900000002"},
	})
	if err != nil || res.IsError || len(reached) != 1 || reached[0] != "media.list" {
		t.Fatalf("narrowed list: %v %+v reached %v", err, res, reached)
	}
}
