package mcpbridge

import (
	"context"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// TestScheduleList_OpenViewReadOnlyNoResolve: the tool forwards the open
// view with the 50 limit, is a read-scope tool in every send mode, shows the
// row's hint and overdue flag untouched, and no tool resolves a row.
func TestScheduleList_OpenViewReadOnlyNoResolve(t *testing.T) {
	t.Parallel()
	row := `{"schedules":[{"id":"u1","state":"send_uncertain","hint":"may have been sent: check the chat"},{"id":"p1","state":"pending","overdue":true}]}`
	for _, cfg := range []Config{{Scope: ScopeRead}, {Scope: ScopeAdmin, SendMode: SendModeDeny}, {Scope: ScopeAdmin, SendMode: SendModeDirect}} {
		fc := &fakeCaller{result: []byte(row)}
		cs := session(t, fc, cfg)
		names := toolNames(t, cs)
		if !names["wa_schedule_list"] {
			t.Fatalf("%+v: wa_schedule_list not registered", cfg)
		}
		for n := range names {
			if strings.Contains(n, "resolve") && n != "wa_resolve_contact" {
				t.Errorf("%+v: tool %s lets an agent close a row", cfg, n)
			}
		}
		lt, _ := cs.ListTools(context.Background(), nil)
		for _, tl := range lt.Tools {
			if tl.Name == "wa_schedule_list" && !strings.Contains(tl.Description, "wa_get_thread") {
				t.Errorf("%+v: description must send the agent to wa_get_thread: %q", cfg, tl.Description)
			}
		}
		res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: "wa_schedule_list", Arguments: map[string]any{}})
		if err != nil || res.IsError {
			t.Fatalf("%+v: %v %+v", cfg, err, res)
		}
		text := res.Content[0].(*mcp.TextContent).Text
		if !strings.Contains(text, "may have been sent: check the chat") || !strings.Contains(text, `"overdue":true`) {
			t.Errorf("%+v: hint or overdue lost: %s", cfg, text)
		}
		fc.mu.Lock()
		calls := append([]recordedCall(nil), fc.calls...)
		fc.mu.Unlock()
		if len(calls) != 1 || calls[0].method != "schedule.list" || calls[0].params["state"] != "open" || calls[0].params["limit"] != float64(50) {
			t.Errorf("%+v: calls %+v, want one schedule.list {state:open, limit:50}", cfg, calls)
		}
	}
	// schedule.list is a read method, so the tool is read scope.
	if toolScopes["wa_schedule_list"] != ScopeRead {
		t.Error("wa_schedule_list must be read scope")
	}
}

// p8 L2/I3: the agent view swaps the daemon-host mediaPath for hasMedia and
// caps lastError; recipient and body stay (same as schedule.list at read scope).
func TestScheduleList_NoDaemonPathAndCappedError(t *testing.T) {
	t.Parallel()
	long := strings.Repeat("e", 500)
	row := `{"schedules":[{"id":"m1","state":"pending","recipient":"r","body":"b","mediaPath":"/var/lib/wa/x.png","lastError":"` + long + `"},{"id":"t1","state":"pending","body":"b"}]}`
	cs := session(t, &fakeCaller{result: []byte(row)}, Config{Scope: ScopeRead})
	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: "wa_schedule_list", Arguments: map[string]any{}})
	if err != nil || res.IsError {
		t.Fatal(err, res)
	}
	text := res.Content[0].(*mcp.TextContent).Text
	if strings.Contains(text, "mediaPath") || strings.Contains(text, "/var/lib") {
		t.Errorf("daemon path leaked: %s", text)
	}
	if strings.Count(text, `"hasMedia":true`) != 1 || strings.Contains(text, long) || !strings.Contains(text, `"recipient":"r"`) || !strings.Contains(text, `"body":"b"`) {
		t.Errorf("want one hasMedia, capped lastError, recipient and body kept: %s", text)
	}
}
