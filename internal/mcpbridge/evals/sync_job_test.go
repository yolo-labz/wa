package evals

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/yolo-labz/wa/v2/internal/mcpbridge"
)

// f6: a long operation must not hold the request. wa_sync_history starts
// sync.force as a job and answers its job_id (with the gap verdict) at
// once; the description sends the agent to wa_job_status, never to a retry.
func evalSyncReturnsJobID(t *testing.T) {
	var mu sync.Mutex
	var method string
	var params map[string]any
	caller := mcpbridge.Caller(func(_ context.Context, m string, p any) (json.RawMessage, *mcpbridge.RPCError, error) {
		mu.Lock()
		defer mu.Unlock()
		method, params = m, p.(map[string]any)
		return json.RawMessage(`{"job_id":"j-f6","state":"queued","gap_reachable":false,"reason":"tail_gap_unanchored"}`), nil, nil
	})
	sess := evalSession(t, caller, mcpbridge.Config{})
	res, err := sess.CallTool(t.Context(), &mcp.CallToolParams{
		Name:      "wa_sync_history",
		Arguments: map[string]any{"chat": "15550000001@s.whatsapp.net", "count": 50, "older_history": true},
	})
	if err != nil || res.IsError {
		t.Fatalf("wa_sync_history: %v %+v", err, res)
	}
	var out map[string]any
	structured(t, res, &out)
	if out["job_id"] != "j-f6" || out["gap_reachable"] != false || out["reason"] != "tail_gap_unanchored" {
		t.Errorf("result = %v, want the job id and the gap verdict", out)
	}
	mu.Lock()
	defer mu.Unlock()
	if method != "sync.force" || params["async"] != true || params["chat"] != "15550000001@s.whatsapp.net" ||
		params["older_history"] != true || params["count"] != float64(50) && params["count"] != 50 {
		t.Errorf("forwarded %s %v, want sync.force with async:true and the arguments", method, params)
	}
	tools, err := sess.ListTools(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, tool := range tools.Tools {
		if tool.Name != "wa_sync_history" {
			continue
		}
		for _, want := range []string{"job_id", "wa_job_status", "gap_reachable", "never", "allowlist", "not_allowlisted"} {
			if !strings.Contains(strings.ToLower(tool.Description), want) {
				t.Errorf("description lacks %q: %s", want, tool.Description)
			}
		}
		return
	}
	t.Fatal("wa_sync_history is not registered")
}
