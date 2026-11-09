package mcpbridge

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/yolo-labz/wa/v2/internal/domain"
)

// remediations is the agent-facing instruction for every reason in the
// closed enum: what happened and which action unblocks it. Codes collide
// (-32100, -32099), so the text is keyed by error.data.reason only.
var remediations = map[domain.ErrorReason]string{
	domain.ReasonMessageNotFound:   "No stored message has this id. List candidates with wa_get_thread or wa_search_messages and use an id from the result; do not assume the media expired.",
	domain.ReasonMediaNotFound:     "The media object is not in the daemon's cache. Re-read the message with wa_get_thread and fetch it again by its message id.",
	domain.ReasonMediaNotCached:    "The message is known but its media reference was never cached. Ask the operator to run `wa sync force --chat <chat>`, then retry once.",
	domain.ReasonMediaUnsupported:  "This message has no downloadable media (text, reaction or an unsupported type). Pick a media message instead; retrying will not help.",
	domain.ReasonMediaTooLarge:     "The payload is over its size cap. Send a shorter text or a smaller file, or ask the operator to fetch large media with `wa media fetch`.",
	domain.ReasonNotAllowlisted:    "Not allowlisted for this action. Ask the operator to grant it in /admin/allowlist or with `wa allow add`; do not retry until they confirm.",
	domain.ReasonPolicyRefused:     "Refused by the daemon's safety policy (for example a blocked recipient, a closed edit window, a missing group-admin right or a broadcast list). Do not retry the same call.",
	domain.ReasonScopeInsufficient: "This token's scope cannot make the call. Ask the operator for a token with the required scope; do not retry with this one.",
	domain.ReasonNotPaired:         "The daemon has no paired WhatsApp session. Ask the operator to pair it (`wa pair`); no call can succeed until then.",
	domain.ReasonSessionWiped:      "The session store was wiped in this daemon process. Ask the operator to restart the daemon and pair again.",
	domain.ReasonDisconnected:      "The daemon is not connected to WhatsApp or is shutting down. Check wa_status and retry once it reports connected.",
	domain.ReasonRateLimited:       "The daemon enforces non-overridable send budgets (per-second/minute/day). Wait before retrying; do not attempt to bypass.",
	domain.ReasonWarmupActive:      "The session is in its warmup ramp, which caps sends on fresh sessions. Wait and retry later; do not attempt to bypass.",
	domain.ReasonInvalidParams:     "The daemon rejected the arguments; the message names the field or the value to use instead. Fix them and retry.",
	domain.ReasonStorageUnwritable: "The daemon cannot write its database. Ask the operator to check storage (wa_status shows it); do not resend.",
	domain.ReasonJobNotFound:       "No job with this id is visible to this caller. Use the job_id from the original response.",
	domain.ReasonTimedOut:          "The call did not finish within its deadline. Poll again; do not resend.",
	domain.ReasonCapacityExceeded:  "The daemon is at capacity. Wait briefly and retry once.",
	domain.ReasonUpstreamError:     "A dependency failed or is not enabled on this daemon (WhatsApp, an adapter, a flag-gated method). Check wa_status, retry once, then ask the operator.",
	domain.ReasonInternal:          "Internal daemon error; the detail is in the daemon log. Check wa_status, retry at most once, then report it to the operator.",
}

// ToolError is a daemon refusal as an MCP tool-execution error (SEP-1303).
// Its JSON form is the tool result's structured content.
type ToolError struct {
	Code          int                `json:"code"`
	Reason        domain.ErrorReason `json:"reason"`
	Remediation   string             `json:"remediation"`
	RequiredScope string             `json:"required_scope,omitempty"`
	text          string
}

func (e *ToolError) Error() string { return e.text }

// remediate converts a daemon JSON-RPC refusal into a ToolError. It maps
// only from error.data.reason: a missing, malformed or unknown reason is
// internal, whatever the code or the message say.
func remediate(e *RPCError) error {
	var data domain.ErrorData
	if len(e.Data) > 0 && json.Unmarshal(e.Data, &data) != nil {
		data = domain.ErrorData{}
	}
	te := &ToolError{Code: e.Code, Reason: domain.ParseErrorReason(string(data.Reason))}
	te.Remediation = remediations[te.Reason]
	switch te.Reason {
	case domain.ReasonScopeInsufficient:
		te.RequiredScope = data.RequiredScope
		te.text = fmt.Sprintf("%s (%d): required scope %q. %s", te.Reason, te.Code, te.RequiredScope, te.Remediation)
	case domain.ReasonNotAllowlisted:
		// FR-050: constant body, whatever the daemon message held.
		te.text = fmt.Sprintf("%s (%d): %s", te.Reason, te.Code, te.Remediation)
	default:
		te.text = fmt.Sprintf("%s (%d): %s. %s", te.Reason, te.Code, e.Message, te.Remediation)
	}
	return te
}

// addTool registers a tool whose ToolError refusals come back as a tool
// result with IsError set and the ToolError as structured content, instead
// of the SDK's text-only flattening of a handler error.
func addTool[In any](srv *mcp.Server, t *mcp.Tool, h mcp.ToolHandlerFor[In, any]) {
	mcp.AddTool(srv, t, func(ctx context.Context, req *mcp.CallToolRequest, in In) (*mcp.CallToolResult, any, error) {
		res, out, err := h(ctx, req, in)
		var te *ToolError
		if errors.As(err, &te) {
			return &mcp.CallToolResult{IsError: true, Content: []mcp.Content{&mcp.TextContent{Text: te.Error()}}}, te, nil
		}
		return res, out, err
	})
}
