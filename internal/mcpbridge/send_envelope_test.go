package mcpbridge

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func resultText(res *mcp.CallToolResult) string {
	var b strings.Builder
	for _, c := range res.Content {
		if t, ok := c.(*mcp.TextContent); ok {
			b.WriteString(t.Text)
		}
	}
	return b.String()
}

// sendCalls are every send-class tool the bridge exposes (react, edit,
// revoke and draft approval have no tool by design) and the tool that polls
// their job.
var sendCalls = map[string]map[string]any{
	"wa_send_message": {"to": "5511999990001", "body": "oi"},
	"wa_send_media":   {"to": "5511999990001", "path": "/srv/media/a.png"},
	"wa_job_status":   {"job_id": "j1"},
}

func callSend(t *testing.T, fc *fakeCaller, tool string) *mcp.CallToolResult {
	t.Helper()
	res, err := session(t, fc, Config{SendMode: SendModeDirect}).CallTool(context.Background(), &mcp.CallToolParams{Name: tool, Arguments: sendCalls[tool]})
	if err != nil {
		t.Fatalf("%s: %v", tool, err)
	}
	return res
}

// TestSendEnvelope_ResultTextSaysStillRunningDoNotResend: a send promoted
// past the 45 s deadline answers {job_id, timed_out:true}; the tool RESULT
// text (not only the docs) tells the agent it is still running and not to
// resend, and the structured payload is unchanged.
func TestSendEnvelope_ResultTextSaysStillRunningDoNotResend(t *testing.T) {
	t.Parallel()
	for _, tool := range []string{"wa_send_message", "wa_send_media"} {
		res := callSend(t, &fakeCaller{result: json.RawMessage(`{"job_id":"j1","state":"running","timed_out":true}`)}, tool)
		text := resultText(res)
		for _, want := range []string{"still running", "j1", "wa_job_status", "DO NOT resend"} {
			if !strings.Contains(text, want) {
				t.Errorf("%s envelope text %q lacks %q", tool, text, want)
			}
		}
		if out, _ := res.StructuredContent.(map[string]any); res.IsError || out["job_id"] != "j1" || out["timed_out"] != true {
			t.Errorf("%s structured = %v (isError %v), want the envelope unchanged", tool, res.StructuredContent, res.IsError)
		}
		if plain := resultText(callSend(t, &fakeCaller{result: json.RawMessage(`{"messageId":"m1","job_id":"x"}`)}, tool)); strings.Contains(plain, "DO NOT resend") {
			t.Errorf("%s: a normal result carries the envelope warning: %q", tool, plain)
		}
	}
}

// TestSendUncertain_ToolErrorNamesTheMessageAndTheCheck: error.data.reason
// send_uncertain (-32023) is a known reason (not "internal", whose hint says
// retry) and the text names the message id, the lookup tools, and no blind
// resend, on the send tools and on the job poll a promoted send ends in.
func TestSendUncertain_ToolErrorNamesTheMessageAndTheCheck(t *testing.T) {
	t.Parallel()
	for tool := range sendCalls {
		for data, wantID := range map[string]string{
			`{"reason":"send_uncertain","message_id":"3EB0ABC123"}`: "3EB0ABC123",
			`{"reason":"send_uncertain"}`:                           "(unknown)",
		} {
			fc := &fakeCaller{rpcErr: &RPCError{Code: -32023, Message: "daemon text withheld", Data: json.RawMessage(data)}}
			res := callSend(t, fc, tool)
			text := resultText(res)
			if !res.IsError {
				t.Errorf("%s: not an error: %q", tool, text)
			}
			for _, want := range []string{"send_uncertain", "message id " + wantID, "may have been sent", "wa_get_thread", "wa_search_messages", "before any resend"} {
				if !strings.Contains(text, want) {
					t.Errorf("%s text %q lacks %q", tool, text, want)
				}
			}
			if out, _ := res.StructuredContent.(map[string]any); out["reason"] != "send_uncertain" || (wantID != "(unknown)" && out["message_id"] != wantID) {
				t.Errorf("%s structured = %v", tool, res.StructuredContent)
			}
			if out, _ := res.StructuredContent.(map[string]any); !strings.Contains(out["remediation"].(string), "before any resend") {
				t.Errorf("%s remediation = %v, want the lookup instruction", tool, out["remediation"])
			}
			if strings.Contains(text, "retry") || strings.Contains(text, "daemon text") {
				t.Errorf("%s text suggests a retry or leaks the daemon message: %q", tool, text)
			}
		}
	}
}

// TestSendTools_DescriptionStatesTheRetryContract: the tool listing itself
// carries the contract; the draft-mode tools do not send, so they do not.
func TestSendTools_DescriptionStatesTheRetryContract(t *testing.T) {
	t.Parallel()
	for mode, want := range map[string]bool{SendModeDirect: true, SendModeDraft: false} {
		tools, err := session(t, &fakeCaller{}, Config{SendMode: mode}).ListTools(context.Background(), nil)
		if err != nil {
			t.Fatal(err)
		}
		for _, tl := range tools.Tools {
			if tl.Name != "wa_send_message" && tl.Name != "wa_send_media" {
				continue
			}
			if got := strings.Contains(tl.Description, "DO NOT resend") && strings.Contains(tl.Description, "send_uncertain"); got != want {
				t.Errorf("%s in mode %v: contract in description = %v, want %v", tl.Name, mode, got, want)
			}
		}
	}
}

// TestSendUncertain_FailedJobResultCarriesTheText: a promoted send that ends
// send_uncertain is polled as a job RESULT {state:failed, error:{reason,
// message_id}}, not an RPC error, so its text must say it too.
func TestSendUncertain_FailedJobResultCarriesTheText(t *testing.T) {
	t.Parallel()
	job := `{"job_id":"j1","state":"failed","error":{"code":-32023,"reason":"send_uncertain","message_id":"3EB0ABC123"}}`
	res := callSend(t, &fakeCaller{result: json.RawMessage(job)}, "wa_job_status")
	text := resultText(res)
	for _, want := range []string{"j1", "(-32023)", "message id 3EB0ABC123", "may have been sent", "wa_get_thread", "before any resend"} {
		if !strings.Contains(text, want) {
			t.Errorf("failed-job text %q lacks %q", text, want)
		}
	}
	if out, _ := res.StructuredContent.(map[string]any); res.IsError || out["state"] != "failed" {
		t.Errorf("structured = %v (isError %v), want the job unchanged", res.StructuredContent, res.IsError)
	}
	for _, plain := range []string{
		`{"job_id":"j1","state":"failed","error":{"code":-32100,"reason":"not_allowlisted"}}`,
		`{"job_id":"j1","state":"done","result":{"messageId":"m"}}`,
		`{"job_id":"j1","state":"running"}`,
	} {
		if text := resultText(callSend(t, &fakeCaller{result: json.RawMessage(plain)}, "wa_job_status")); strings.Contains(text, "may have been sent") {
			t.Errorf("%s carries the uncertain text: %q", plain, text)
		}
	}
}
