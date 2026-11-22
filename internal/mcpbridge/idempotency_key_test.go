package mcpbridge

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// keyedCalls maps each tool that takes idempotency_key to the RPC it forwards
// to (all three are idempotentCall methods in the daemon).
var keyedCalls = map[string]struct {
	method string
	args   map[string]any
}{
	"wa_send_message":     {"send", map[string]any{"to": "5511000000000", "body": "hi"}},
	"wa_send_media":       {"sendMedia", map[string]any{"to": "5511000000000", "path": "/tmp/a.png"}},
	"wa_schedule_message": {"schedule.send", map[string]any{"to": "5511000000000", "body": "hi", "send_at": time.Now().Add(time.Hour).Format(time.RFC3339)}},
}

func callKeyed(t *testing.T, fc *fakeCaller, tool, key string) *mcp.CallToolResult {
	t.Helper()
	args := map[string]any{}
	for k, v := range keyedCalls[tool].args {
		args[k] = v
	}
	if key != "" {
		args["idempotency_key"] = key
	}
	res, err := session(t, fc, Config{Scope: ScopeAdmin, SendMode: SendModeDirect}).CallTool(context.Background(), &mcp.CallToolParams{Name: tool, Arguments: args})
	if err != nil {
		t.Fatalf("%s: %v", tool, err)
	}
	return res
}

// TestIdempotencyKey_ReachesTheRPC: a valid key is forwarded as the RPC's
// idempotencyKey; no key forwards none.
func TestIdempotencyKey_ReachesTheRPC(t *testing.T) {
	t.Parallel()
	for tool, k := range keyedCalls {
		fc := &fakeCaller{}
		callKeyed(t, fc, tool, "msg-42_A")
		if got := fc.last(t); got.method != k.method || got.params["idempotencyKey"] != "msg-42_A" {
			t.Errorf("%s: call %s %v, want %s with idempotencyKey msg-42_A", tool, got.method, got.params, k.method)
		}
		fc = &fakeCaller{}
		callKeyed(t, fc, tool, "")
		if _, has := fc.last(t).params["idempotencyKey"]; has {
			t.Errorf("%s: sent an idempotencyKey without one", tool)
		}
	}
}

// TestIdempotencyKey_MalformedIsInvalidParamsAndSendsNothing: too long, wrong
// charset; refused before any daemon call.
func TestIdempotencyKey_MalformedIsInvalidParamsAndSendsNothing(t *testing.T) {
	t.Parallel()
	for tool := range keyedCalls {
		for _, bad := range []string{strings.Repeat("a", 65), "has space", "a/b", "a.b", "é"} {
			fc := &fakeCaller{}
			res := callKeyed(t, fc, tool, bad)
			if !res.IsError || !strings.Contains(resultText(res), "idempotency_key") {
				t.Errorf("%s key %q: result %q (isError %v), want an idempotency_key refusal", tool, bad, resultText(res), res.IsError)
			}
			if out, _ := res.StructuredContent.(map[string]any); out["reason"] != "invalid_params" {
				t.Errorf("%s key %q: structured %v, want reason invalid_params", tool, bad, res.StructuredContent)
			}
			if len(fc.calls) != 0 {
				t.Errorf("%s key %q reached the daemon: %v", tool, bad, fc.calls)
			}
		}
	}
	if res := callKeyed(t, &fakeCaller{}, "wa_send_message", strings.Repeat("a", 64)); res.IsError {
		t.Errorf("a 64-char key was refused: %q", resultText(res))
	}
}

// TestIdempotencyKey_DescriptionStatesTheContract: the send tools tell the
// agent to set a key per intended message.
func TestIdempotencyKey_DescriptionStatesTheContract(t *testing.T) {
	t.Parallel()
	tools, err := session(t, &fakeCaller{}, Config{SendMode: SendModeDirect}).ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, tl := range tools.Tools {
		if tl.Name == "wa_send_message" || tl.Name == "wa_send_media" {
			if !strings.Contains(tl.Description, "idempotency_key") || !strings.Contains(tl.Description, "same key = same send") {
				t.Errorf("%s description lacks the key contract: %q", tl.Name, tl.Description)
			}
		}
	}
}
