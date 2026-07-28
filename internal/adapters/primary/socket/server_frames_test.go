package socket

import (
	"encoding/json"
	"testing"
)

// decodeParams unwraps a notification frame down to its params object.
func decodeParams(t *testing.T, frame []byte) map[string]json.RawMessage {
	t.Helper()
	var outer struct {
		JSONRPC string                     `json:"jsonrpc"`
		Method  string                     `json:"method"`
		Params  map[string]json.RawMessage `json:"params"`
	}
	if err := json.Unmarshal(frame, &outer); err != nil {
		t.Fatalf("unmarshal frame: %v", err)
	}
	if outer.JSONRPC != "2.0" || outer.Method != "event" {
		t.Fatalf("frame envelope = %q/%q, want 2.0/event", outer.JSONRPC, outer.Method)
	}
	return outer.Params
}

// TestMarshalNotificationMergesPayload is the regression this file exists
// for: the frame builder used to emit the envelope alone, so `wa subscribe`
// printed the event kind and nothing else — no body, no sender, no
// timestamp. A subscriber could tell that *something* arrived and never
// what.
func TestMarshalNotificationMergesPayload(t *testing.T) {
	t.Parallel()

	payload := struct {
		ID      string `json:"id"`
		TS      int64  `json:"ts"`
		Chat    string `json:"chat"`
		Channel string `json:"channel"`
	}{ID: "e1", TS: 1781000000, Chat: "555@s.whatsapp.net", Channel: "<channel source=\"wa\">hi</channel>"}

	frame, err := marshalNotification(Event{Type: "message", Payload: payload}, "sub-1")
	if err != nil {
		t.Fatalf("marshalNotification: %v", err)
	}
	params := decodeParams(t, frame)

	for _, key := range []string{"id", "ts", "chat", "channel"} {
		if _, ok := params[key]; !ok {
			t.Errorf("params missing payload field %q; got keys %v", key, keysOf(params))
		}
	}
	assertStringField(t, params, "type", "message")
	assertStringField(t, params, "subscriptionId", "sub-1")
	assertStringField(t, params, "schema", "wa.event/v1")
}

// TestMarshalNotificationEnvelopeWinsCollision pins the merge order. The
// payload is attacker-influenced at one remove (its fields come from an
// inbound message), so a field named "type" must not be able to relabel
// the event kind the subscription filtered on.
func TestMarshalNotificationEnvelopeWinsCollision(t *testing.T) {
	t.Parallel()

	payload := map[string]any{
		"type":           "status",
		"schema":         "attacker/v1",
		"subscriptionId": "sub-evil",
		"seq":            99,
		"keep":           "me",
	}
	frame, err := marshalNotification(Event{Type: "message", Seq: 7, Payload: payload}, "sub-1")
	if err != nil {
		t.Fatalf("marshalNotification: %v", err)
	}
	params := decodeParams(t, frame)

	assertStringField(t, params, "type", "message")
	assertStringField(t, params, "schema", "wa.event/v1")
	assertStringField(t, params, "subscriptionId", "sub-1")
	assertStringField(t, params, "keep", "me")
	if got := string(params["seq"]); got != "7" {
		t.Errorf("seq = %s, want 7 (envelope value, not the payload's)", got)
	}
}

// TestMarshalNotificationNonObjectPayload covers the shape that cannot be
// flattened. No producer emits one today, but dropping it silently is the
// bug this change fixes, so it has to stay visible under its own key.
func TestMarshalNotificationNonObjectPayload(t *testing.T) {
	t.Parallel()

	frame, err := marshalNotification(Event{Type: "odd", Payload: "just a string"}, "sub-1")
	if err != nil {
		t.Fatalf("marshalNotification: %v", err)
	}
	params := decodeParams(t, frame)
	if got := string(params["payload"]); got != `"just a string"` {
		t.Errorf("payload = %s, want the quoted string", got)
	}
	assertStringField(t, params, "type", "odd")
}

// TestMarshalNotificationNilPayload keeps the pre-existing envelope-only
// frame valid for events that genuinely carry nothing, and asserts no
// stray "payload": null key appears.
func TestMarshalNotificationNilPayload(t *testing.T) {
	t.Parallel()

	frame, err := marshalNotification(Event{Type: "status"}, "sub-1")
	if err != nil {
		t.Fatalf("marshalNotification: %v", err)
	}
	params := decodeParams(t, frame)
	if _, ok := params["payload"]; ok {
		t.Errorf("nil payload produced a payload key: %v", keysOf(params))
	}
	assertStringField(t, params, "type", "status")
	if _, ok := params["seq"]; ok {
		t.Errorf("zero Seq must stay off the wire; got keys %v", keysOf(params))
	}
}

func assertStringField(t *testing.T, params map[string]json.RawMessage, key, want string) {
	t.Helper()
	var got string
	if err := json.Unmarshal(params[key], &got); err != nil {
		t.Fatalf("unmarshal params[%q]: %v", key, err)
	}
	if got != want {
		t.Errorf("params[%q] = %q, want %q", key, got, want)
	}
}

func keysOf(m map[string]json.RawMessage) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
