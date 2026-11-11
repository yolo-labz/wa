package mcpbridge

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/yolo-labz/wa/v2/internal/app"
	"github.com/yolo-labz/wa/v2/internal/domain"
)

// reasonData is the error.data member a transport would deliver.
func reasonData(t *testing.T, d domain.ErrorData) json.RawMessage {
	t.Helper()
	b, err := json.Marshal(d)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func toolErr(t *testing.T, e *RPCError) *ToolError {
	t.Helper()
	var te *ToolError
	if !errors.As(remediate(e), &te) {
		t.Fatalf("remediate(%+v) is not a *ToolError", e)
	}
	return te
}

// callStructured calls wa_get_thread against a bridge whose daemon refuses
// with rpcErr and returns the tool result's structured content.
func callStructured(t *testing.T, rpcErr *RPCError) (ToolError, string) {
	t.Helper()
	cs := session(t, &fakeCaller{rpcErr: rpcErr}, Config{})
	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{
		Name: "wa_get_thread", Arguments: map[string]any{"chat": "chat-a"},
	})
	if err != nil {
		t.Fatalf("refusal must be a tool error, got protocol error: %v", err)
	}
	if !res.IsError || len(res.Content) != 1 {
		t.Fatalf("result = %+v, want one IsError content block", res)
	}
	raw, err := json.Marshal(res.StructuredContent)
	if err != nil {
		t.Fatal(err)
	}
	var out ToolError
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("structured content %s: %v", raw, err)
	}
	return out, res.Content[0].(*mcp.TextContent).Text
}

// TestRemediate_EveryCatalogCode walks every RPCWire row through the
// bridge: each reaches the agent as structured {code, reason, remediation}
// with the row's own reason and that reason's remediation, and every
// member of the closed enum has a distinct, non-empty remediation.
func TestRemediate_EveryCatalogCode(t *testing.T) {
	t.Parallel()
	seen := map[string]domain.ErrorReason{}
	for _, r := range domain.ErrorReasons() {
		text := remediations[r]
		if strings.TrimSpace(text) == "" {
			t.Errorf("reason %q has no remediation", r)
		}
		if prev, dup := seen[text]; dup {
			t.Errorf("reasons %q and %q share a remediation", prev, r)
		}
		seen[text] = r
	}
	if len(remediations) != len(domain.ErrorReasons()) {
		t.Errorf("remediation table has %d rows for %d reasons", len(remediations), len(domain.ErrorReasons()))
	}

	for _, row := range app.RPCWireErrors() {
		code, msg, reason := app.RPCWire(fmt.Errorf("call: %w", row))
		in := &RPCError{Code: int(code), Message: msg, Data: reasonData(t, domain.ErrorData{Reason: reason})}
		got, text := callStructured(t, in)
		if got.Code != int(code) || got.Reason != reason || got.Remediation != remediations[reason] {
			t.Errorf("%v: structured %+v, want code %d reason %q", row, got, code, reason)
		}
		if got.RequiredScope != "" {
			t.Errorf("%v: required_scope %q outside scope_insufficient", row, got.RequiredScope)
		}
		if !strings.Contains(text, string(reason)) || !strings.Contains(text, got.Remediation) {
			t.Errorf("%v: text %q lacks reason or remediation", row, text)
		}
	}

	got, text := callStructured(t, &RPCError{
		Code: -32099, Message: "scope insufficient for method",
		Data: reasonData(t, domain.ErrorData{Reason: domain.ReasonScopeInsufficient, RequiredScope: "admin", GrantedScope: "send"}),
	})
	if got.Reason != domain.ReasonScopeInsufficient || got.RequiredScope != "admin" || !strings.Contains(text, `"admin"`) {
		t.Errorf("scope refusal: %+v / %q", got, text)
	}
}

// TestRemediate_NoNumericBands pins that the code band carries no meaning:
// without a reason every code is internal, and a reason means the same
// thing under every code. -32117 message-not-found was told "the recipient
// is not allowlisted" by the old -32199..-32100 band (F1).
func TestRemediate_NoNumericBands(t *testing.T) {
	t.Parallel()
	codes := []int{-32000, -32012, -32050, -32099, -32100, -32117, -32150, -32199, -32200, -32250, -32299, -32300, -32301, -32601, -32603}
	for _, code := range codes {
		bare := toolErr(t, &RPCError{Code: code, Message: "x"})
		if bare.Reason != domain.ReasonInternal || bare.Remediation != remediations[domain.ReasonInternal] {
			t.Errorf("code %d without reason: %+v, want internal", code, bare)
		}
		typed := toolErr(t, &RPCError{
			Code: code, Message: "message not found",
			Data: reasonData(t, domain.ErrorData{Reason: domain.ReasonMessageNotFound}),
		})
		if typed.Reason != domain.ReasonMessageNotFound || typed.Remediation != remediations[domain.ReasonMessageNotFound] {
			t.Errorf("code %d with message_not_found: %+v", code, typed)
		}
		if strings.Contains(typed.Error(), "allowlist") {
			t.Errorf("code %d message_not_found mentions the allowlist: %q", code, typed.Error())
		}
	}
}

// TestRemediate_NotAllowlistedBodyConstant pins FR-050 on MCP: whatever
// message the daemon sent, a not_allowlisted refusal reads byte-identical
// and names the human action, so an agent cannot probe recipients.
func TestRemediate_NotAllowlistedBodyConstant(t *testing.T) {
	t.Parallel()
	var first string
	var firstStructured ToolError
	for i, msg := range []string{"PolicyRefused", "not allowlisted: recipient-a", "send to group-b refused", ""} {
		in := &RPCError{Code: -32100, Message: msg, Data: reasonData(t, domain.ErrorData{Reason: domain.ReasonNotAllowlisted})}
		got, text := callStructured(t, in)
		if i == 0 {
			first, firstStructured = text, got
			continue
		}
		if text != first || got != firstStructured {
			t.Errorf("message %q changed the refusal: %q / %+v, want %q / %+v", msg, text, got, first, firstStructured)
		}
	}
	for _, want := range []string{"/admin/allowlist", "wa allow add", "do not retry until they confirm"} {
		if !strings.Contains(first, want) {
			t.Errorf("refusal %q lacks %q", first, want)
		}
	}

	// The daemon side of the same contract: RPCWire's row is constant too.
	codeA, msgA, reasonA := app.RPCWire(fmt.Errorf("send to recipient-a: %w", app.ErrNotAllowlisted))
	codeB, msgB, reasonB := app.RPCWire(fmt.Errorf("group.add recipient-b: %w", app.ErrNotAllowlisted))
	if codeA != -32100 || codeB != codeA || msgA != msgB || reasonA != domain.ReasonNotAllowlisted || reasonB != reasonA {
		t.Errorf("RPCWire not-allowlisted rows differ: (%d %q %q) vs (%d %q %q)", codeA, msgA, reasonA, codeB, msgB, reasonB)
	}
}

// TestRemediate_MissingUnknownReasonIsInternal pins that a missing,
// malformed or unknown reason maps to internal with no fallback to a
// misleading code or message, and never surfaces a stray required_scope.
func TestRemediate_MissingUnknownReasonIsInternal(t *testing.T) {
	t.Parallel()
	for _, data := range []string{
		"", "null", "{}", `{"reason":""}`, `{"reason":"bogus"}`,
		`{"reason":"NOT_ALLOWLISTED"}`, `{"reason":7}`, `not json`, `{"required_scope":"admin"}`,
	} {
		for _, in := range []*RPCError{
			{Code: -32100, Message: "not allowlisted", Data: json.RawMessage(data)},
			{Code: -32099, Message: "scope insufficient for method", Data: json.RawMessage(data)},
		} {
			got := toolErr(t, in)
			if got.Reason != domain.ReasonInternal || got.Remediation != remediations[domain.ReasonInternal] || got.RequiredScope != "" {
				t.Errorf("data %q code %d: %+v, want internal", data, in.Code, got)
			}
			if got.Code != in.Code {
				t.Errorf("data %q: code %d, want the daemon's %d", data, got.Code, in.Code)
			}
		}
	}
}
