// server_frames.go holds the pure JSON-RPC notification frame builders the
// fan-out, heartbeat, and shutdown paths emit. Every function here is
// side-effect-free: bytes in, bytes out. Split from server.go (ARCH-04) —
// see the C-003 architecture note on the Server struct.
package socket

import "encoding/json"

// marshalNotification creates a JSON-RPC 2.0 server notification frame for
// an event.
func marshalNotification(evt Event, subscriptionID string) ([]byte, error) {
	params := map[string]any{
		"schema":         "wa.event/v1",
		"type":           evt.Type,
		"subscriptionId": subscriptionID,
	}
	if evt.Seq > 0 {
		params["seq"] = evt.Seq
	}
	frame := map[string]any{
		"jsonrpc": "2.0",
		"method":  "event",
		"params":  params,
	}
	return json.Marshal(frame)
}

// shutdownFrame returns a JSON-RPC error notification for ShutdownInProgress (-32002).
func shutdownFrame(subscriptionID string) []byte {
	frame := map[string]any{
		"jsonrpc": "2.0",
		"error": map[string]any{
			"code":    int(CodeShutdownInProgress),
			"message": errCodeName[CodeShutdownInProgress],
			"data": map[string]any{
				"subscriptionId": subscriptionID,
			},
		},
	}
	data, _ := json.Marshal(frame)
	return data
}

// subscribePingFrame returns a JSON-RPC notification asking the client to
// acknowledge liveness via subscribe.pong.
func subscribePingFrame(subscriptionID string) []byte {
	frame := map[string]any{
		"jsonrpc": "2.0",
		"method":  "subscribe.ping",
		"params": map[string]any{
			"subscriptionId": subscriptionID,
		},
	}
	data, _ := json.Marshal(frame)
	return data
}

// subscribeClosedPongTimeoutFrame returns a JSON-RPC error notification
// signalling FR-062 pong-timeout closure. resumeSince is the subscription's
// lastSeq at the moment of closure so the client can re-subscribe with a
// Kafka-style cursor.
func subscribeClosedPongTimeoutFrame(subscriptionID string, resumeSince int64) []byte {
	frame := map[string]any{
		"jsonrpc": "2.0",
		"error": map[string]any{
			"code":    int(CodePongTimeout),
			"message": errCodeName[CodePongTimeout],
			"data": map[string]any{
				"subscriptionId": subscriptionID,
				"reason":         "pong_timeout",
				"resumeSince":    resumeSince,
			},
		},
	}
	data, _ := json.Marshal(frame)
	return data
}

// streamDropFrame returns a JSON-RPC error notification signalling FR-063
// ring-buffer gap. count is the number of dropped events inclusive.
func streamDropFrame(subscriptionID string, oldestDropped, newestDropped int64) []byte {
	count := newestDropped - oldestDropped + 1
	frame := map[string]any{
		"jsonrpc": "2.0",
		"error": map[string]any{
			"code":    int(CodeStreamDrop),
			"message": errCodeName[CodeStreamDrop],
			"data": map[string]any{
				"subscriptionId": subscriptionID,
				"count":          count,
				"oldest_dropped": oldestDropped,
				"newest_dropped": newestDropped,
			},
		},
	}
	data, _ := json.Marshal(frame)
	return data
}

// subscriptionClosedFrame returns a JSON-RPC error notification for
// SubscriptionClosed (-32005).
func subscriptionClosedFrame(subscriptionID string) []byte {
	frame := map[string]any{
		"jsonrpc": "2.0",
		"error": map[string]any{
			"code":    int(CodeSubscriptionClosed),
			"message": errCodeName[CodeSubscriptionClosed],
			"data": map[string]any{
				"subscriptionId": subscriptionID,
			},
		},
	}
	data, _ := json.Marshal(frame)
	return data
}
