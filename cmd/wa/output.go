package main

import (
	"encoding/json"
	"fmt"
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
			return fmt.Sprintf("Connected as %s", jid)
		}
		return "Not connected"

	case "send", "sendMedia":
		msgID, _ := obj["messageId"].(string)
		return fmt.Sprintf("Sent: %s", msgID)

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
		return fmt.Sprintf("wa version %s", v)

	case "groups":
		groups, _ := obj["groups"].([]any)
		return fmt.Sprintf("%d groups", len(groups))

	default:
		out, _ := json.MarshalIndent(obj, "", "  ")
		return string(out)
	}
}
