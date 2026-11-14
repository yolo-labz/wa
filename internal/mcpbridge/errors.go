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
	domain.ReasonSendUncertain:     "The message MAY have been delivered: the daemon sent it but never got the confirmation. Do not resend. Check the thread (wa_get_thread) for the id in the message, or poll the job; only the operator decides on a resend.",
	domain.ReasonInternal:          "Internal daemon error; the detail is in the daemon log. Check wa_status, retry at most once, then report it to the operator.",
}

// ToolError is a daemon refusal as an MCP tool-execution error (SEP-1303).
// Its JSON form is the tool result's structured content.
type ToolError struct {
	Code          int                `json:"code"`
	Reason        domain.ErrorReason `json:"reason"`
	Remediation   string             `json:"remediation"`
	RequiredScope string             `json:"required_scope,omitempty"`
	Size          int64              `json:"size_bytes,omitempty"`
	MessageID     string             `json:"message_id,omitempty"`
	text          string
}

func (e *ToolError) Error() string { return e.text }

// Remediation is the agent-facing instruction for reason, so a daemon
// method that answers a refusal in its result (allow.check) uses the
// same constant text as the bridge.
func Remediation(reason domain.ErrorReason) string {
	return remediations[domain.ParseErrorReason(string(reason))]
}

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
	case domain.ReasonSendUncertain:
		// The agent needs the id to find the message; it rides in
		// error.data, so the daemon's text stays withheld.
		te.MessageID = data.MessageID
		te.text = fmt.Sprintf("%s (%d): message %q. %s", te.Reason, te.Code, te.MessageID, te.Remediation)
	case domain.ReasonInvalidParams:
		// The remediation tells the agent the message names the field.
		te.text = fmt.Sprintf("%s (%d): %s. %s", te.Reason, te.Code, e.Message, te.Remediation)
	default:
		// The daemon message is withheld: RPCWire's domain rows carry the
		// whole error chain, which can name daemon paths or other chats.
		// not_allowlisted (FR-050) relies on this constant body too.
		te.text = fmt.Sprintf("%s (%d): %s", te.Reason, te.Code, te.Remediation)
	}
	return te
}

// localError is a refusal the bridge makes itself, typed like a daemon
// refusal so every tool error carries {code, reason, remediation}
// (FR-116-10). msg is constant or names only the caller's own field: it
// never wraps a Go error, which can carry socket paths or parser internals.
func localError(code int, reason domain.ErrorReason, msg string) error {
	return &ToolError{Code: code, Reason: reason, Remediation: remediations[reason], text: fmt.Sprintf("%s (%d): %s. %s", reason, code, msg, remediations[reason])}
}

// errDaemonUnreachable answers a transport failure. The dial error (socket
// path, errno) is withheld from the agent; the operator sees it in wa status.
var (
	errDaemonUnreachable = localError(-32018, domain.ReasonDisconnected, "the daemon is unreachable (is wad running?)")
	errMalformedResult   = localError(-32603, domain.ReasonInternal, "the daemon returned malformed JSON")
)

// addTool registers a tool whose ToolError refusals come back as a tool
// result with IsError set and the ToolError as structured content, instead
// of the SDK's text-only flattening of a handler error.
//
// It also states the tool's required scope in its description and _meta
// (FR-116-11). A tool without a toolScopes entry fails closed: it is not
// registered at all, which the eval snapshot reports as a missing tool.
func addTool[In any](srv *mcp.Server, t *mcp.Tool, h mcp.ToolHandlerFor[In, any]) {
	scope, ok := toolScopes[t.Name]
	if !ok {
		return
	}
	t.Description += " Requires scope: " + scope + "."
	t.Meta = mcp.Meta{"required_scope": scope}
	mcp.AddTool(srv, t, func(ctx context.Context, req *mcp.CallToolRequest, in In) (*mcp.CallToolResult, any, error) {
		res, out, err := h(ctx, req, in)
		var te *ToolError
		if errors.As(err, &te) {
			return &mcp.CallToolResult{IsError: true, Content: []mcp.Content{&mcp.TextContent{Text: te.Error()}}}, te, nil
		}
		return res, out, err
	})
}
