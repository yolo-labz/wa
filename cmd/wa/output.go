package main

import (
	"encoding/json"
	"fmt"
	"time"
)

// formatResult formats the JSON-RPC result for CLI output. If jsonMode
// is true, it wraps the result with a versioned schema string. Otherwise
// it returns a human-readable representation.
func formatResult(method string, result json.RawMessage, jsonMode bool) string {
	if jsonMode {
		return formatJSON(method, result)
	}
	return formatHuman(method, result)
}

// formatJSON wraps the result in a versioned envelope:
// {"schema":"wa.<method>/v1", ...fields from result...}
func formatJSON(method string, result json.RawMessage) string {
	// Parse the result so we can merge the schema field in.
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(result, &obj); err != nil {
		// Fallback: wrap as-is.
		return fmt.Sprintf(`{"schema":"wa.%s/v1","data":%s}`, method, string(result))
	}

	schema := fmt.Sprintf(`"wa.%s/v1"`, method)
	obj["schema"] = json.RawMessage(schema)

	out, err := json.Marshal(obj)
	if err != nil {
		return string(result)
	}
	return string(out)
}

// formatHuman returns a simple human-readable rendering of the result.
func formatHuman(method string, result json.RawMessage) string { //nolint:gocyclo // method-dispatch switch, linear not complex
	var obj map[string]any
	if err := json.Unmarshal(result, &obj); err != nil {
		return string(result)
	}

	switch method {
	case "status":
		connected, _ := obj["connected"].(bool)
		if connected {
			jid, _ := obj["jid"].(string)
			return "Connected as " + jid
		}
		return "Not connected"

	case "send", "sendMedia":
		msgID, _ := obj["messageId"].(string)
		return "Sent: " + msgID

	case "pair":
		paired, _ := obj["paired"].(bool)
		if paired {
			return "Paired successfully"
		}
		return "Pairing failed"

	case "allow":
		if added, _ := obj["added"].(bool); added {
			jid, _ := obj["jid"].(string)
			return fmt.Sprintf("Added %s to allowlist", jid)
		}
		if removed, _ := obj["removed"].(bool); removed {
			jid, _ := obj["jid"].(string)
			return fmt.Sprintf("Removed %s from allowlist", jid)
		}
		// list case is handled directly in cmd_allow.go
		out, _ := json.MarshalIndent(obj, "", "  ")
		return string(out)

	case "panic":
		if unlinked, _ := obj["unlinked"].(bool); unlinked {
			return "Device unlinked and session wiped"
		}
		return "Panic failed"

	case "react":
		return "Reaction sent"

	case "markRead":
		return "Marked as read"

	case "wait":
		return string(result)

	case "version":
		v, _ := obj["version"].(string)
		return "wa version " + v

	case "groups":
		groups, _ := obj["groups"].([]any)
		return fmt.Sprintf("%d groups", len(groups))

	default:
		out, _ := json.MarshalIndent(obj, "", "  ")
		return string(out)
	}
}

// parseTimeFlag converts an RFC3339 --since/--until value to unix seconds.
// An empty value yields 0 (unbounded). On a parse error it returns an
// exitf(64) error carrying the command + flag name. Shared by `wa messages
// list` and `wa export`. Issue #173 / #180.
func parseTimeFlag(cmdName, flag, val string) (int64, error) {
	if val == "" {
		return 0, nil
	}
	t, err := time.Parse(time.RFC3339, val)
	if err != nil {
		return 0, exitf(64, "%s: --%s must be RFC3339 (e.g. 2026-01-31T12:00:00Z), got %q", cmdName, flag, val)
	}
	return t.Unix(), nil
}
