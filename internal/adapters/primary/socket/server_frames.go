// server_frames.go holds the pure JSON-RPC notification frame builders the
// fan-out, heartbeat, and shutdown paths emit. Every function here is
// side-effect-free: bytes in, bytes out. Split from server.go (ARCH-04) —
// see the C-003 architecture note on the Server struct.
package socket

import "encoding/json"

// marshalNotification creates a JSON-RPC 2.0 server notification frame for
// an event: the envelope keys plus the event's own fields, merged into
// one params object per the Event.Payload contract in dispatcher.go.
//
// Payload is merged, not nested, because that is what the wire protocol
// documents and what the SSE transport already does with the same
// projections. Without the merge a socket subscriber received
// {schema, type, subscriptionId} and nothing else — `wa subscribe` and
// `wa stream` printed the event kind with no body, sender or timestamp.
func marshalNotification(evt Event, subscriptionID string) ([]byte, error) {
	params, err := paramsFromPayload(evt.Payload)
	if err != nil {
		return nil, err
	}
	// Envelope keys are assigned AFTER the merge so they always win: a
	// payload field named "type" must not be able to relabel the event
	// kind the subscription filtered on.
	params["schema"] = "wa.event/v1"
	params["type"] = evt.Type
	params["subscriptionId"] = subscriptionID
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

// paramsFromPayload turns an event payload into the base params map the
// envelope keys are then written over.
//
// A payload that marshals to a JSON object is flattened into params. One
// that does not — a bare string, number or array — cannot be flattened,
// so it lands under a "payload" key instead of being dropped: every
// producer in this repo emits an object, and silently discarding the one
// that does not is the failure this function exists to prevent.
func paramsFromPayload(payload any) (map[string]any, error) {
	params := make(map[string]any, 8)
	if payload == nil {
		return params, nil
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		params["payload"] = json.RawMessage(raw)
		return params, nil
	}
	for k, v := range fields {
		params[k] = v
	}
	return params, nil
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
