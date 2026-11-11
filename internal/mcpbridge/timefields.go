package mcpbridge

import (
	"context"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// forwardTimed is forwardResult for the message read tools, plus FR-116-17:
// every unix-seconds time gains its unit-carrying pair and every channel
// envelope says whether it holds a caption. The daemon payload is otherwise
// passed through unchanged; nothing is unwrapped.
func forwardTimed(ctx context.Context, call Caller, method string, params any) (*mcp.CallToolResult, any, error) {
	res, out, err := forwardResult(ctx, call, method, params)
	if err == nil {
		addUnitFields(out)
	}
	return res, out, err
}

// addUnitFields walks a decoded result. Next to a numeric "ts" or
// "timestamp" (unix seconds on every read RPC) it adds "<key>_unix_s" and,
// when the value is set, "<key>_at" in RFC 3339 UTC, so 0 never reads as
// 1970. An object with a "channel" envelope and no caption fields gains
// "has_caption" and the caption alone in its own envelope.
func addUnitFields(v any) {
	switch t := v.(type) {
	case []any:
		for _, e := range t {
			addUnitFields(e)
		}
	case map[string]any:
		for _, k := range []string{"ts", "timestamp"} {
			if n, ok := t[k].(float64); ok {
				t[k+"_unix_s"] = int64(n)
				if n > 0 {
					t[k+"_at"] = time.Unix(int64(n), 0).UTC().Format(time.RFC3339)
				}
			}
		}
		if ch, ok := t["channel"].(string); ok && t["has_caption"] == nil {
			capt := captionEnvelope(ch)
			t["has_caption"] = capt != ""
			if capt != "" {
				t["caption"] = capt
			}
		}
		// The keys added above are scalars, so walking them is a no-op.
		for _, c := range t {
			addUnitFields(c)
		}
	}
}

// captionEnvelope cuts the caption field out of a daemon channel envelope
// into an envelope of its own (same opening tag). Sender text inside the
// envelope has '<' and '>' escaped, so neither the field marker nor the end
// of the opening tag can be forged by the sender.
func captionEnvelope(ch string) string {
	const open, closeTag = `<field name="caption">`, `</field>`
	head := strings.IndexByte(ch, '>')
	i := strings.Index(ch, open)
	if head < 0 || i <= head || !strings.HasPrefix(ch, "<channel ") {
		return ""
	}
	j := strings.Index(ch[i:], closeTag)
	if j < 0 {
		return ""
	}
	return ch[:head+1] + ch[i:i+j+len(closeTag)] + "</channel>"
}
