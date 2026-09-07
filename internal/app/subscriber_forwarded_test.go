package app

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/yolo-labz/wa/v2/internal/domain"
)

// TestSubscriberForwardedReachesTheWire — feature 117. The whole point of
// the field is that a subscriber can drop forwarded messages, so the test
// that matters is on the marshalled JSON a webhook/SSE consumer actually
// receives, not on the Go struct.
func TestSubscriberForwardedReachesTheWire(t *testing.T) {
	t.Parallel()
	chat := subTestJID(t, "12025550100@s.whatsapp.net")

	dto := wrapMessageEventForSubscribers(domain.MessageEvent{
		ID:              "evt-fwd",
		TS:              time.Unix(1781000000, 0),
		From:            chat,
		Message:         domain.TextMessage{Recipient: chat, Body: "corrente"},
		IsForwarded:     true,
		ForwardingScore: 9,
	})

	if !dto.IsForwarded {
		t.Error("IsForwarded did not survive the projection")
	}
	if dto.ForwardingScore != 9 {
		t.Errorf("ForwardingScore = %d, want 9", dto.ForwardingScore)
	}

	raw, err := json.Marshal(dto)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var wire map[string]any
	if err := json.Unmarshal(raw, &wire); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if wire["isForwarded"] != true {
		t.Errorf("isForwarded missing/wrong on the wire: %s", raw)
	}
	if wire["forwardingScore"] != float64(9) {
		t.Errorf("forwardingScore missing/wrong on the wire: %s", raw)
	}
}

// TestSubscriberNotForwardedOmitsFields — omitempty keeps the payload
// byte-identical for the overwhelming majority of messages, which are not
// forwarded. A consumer must read an absent key as "not marked".
func TestSubscriberNotForwardedOmitsFields(t *testing.T) {
	t.Parallel()
	chat := subTestJID(t, "12025550100@s.whatsapp.net")

	dto := wrapMessageEventForSubscribers(domain.MessageEvent{
		ID:      "evt-plain",
		TS:      time.Unix(1781000000, 0),
		From:    chat,
		Message: domain.TextMessage{Recipient: chat, Body: "oi"},
	})

	raw, err := json.Marshal(dto)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var wire map[string]any
	if err := json.Unmarshal(raw, &wire); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if _, ok := wire["isForwarded"]; ok {
		t.Errorf("isForwarded must be omitted when false: %s", raw)
	}
	if _, ok := wire["forwardingScore"]; ok {
		t.Errorf("forwardingScore must be omitted when zero: %s", raw)
	}
}

// TestSubscriberForwardedIsPlainNotChannelWrapped — the flags are
// structural wire facts, not sender prose, so they belong beside messageId
// rather than inside the FR-005a envelope. A consumer that had to parse
// them out of <channel> could not trust them as booleans.
func TestSubscriberForwardedIsPlainNotChannelWrapped(t *testing.T) {
	t.Parallel()
	chat := subTestJID(t, "12025550100@s.whatsapp.net")

	dto := wrapMessageEventForSubscribers(domain.MessageEvent{
		ID:              "evt-fwd2",
		TS:              time.Unix(1781000000, 0),
		From:            chat,
		Message:         domain.TextMessage{Recipient: chat, Body: "x"},
		IsForwarded:     true,
		ForwardingScore: 5,
	})

	for _, needle := range []string{"isForwarded", "forwardingScore"} {
		if containsField(dto.Channel, needle) {
			t.Errorf("%s must not be channel-wrapped; envelope = %s", needle, dto.Channel)
		}
	}
}

func containsField(channel, name string) bool {
	return len(channel) > 0 && len(name) > 0 &&
		func() bool {
			needle := `<field name="` + name + `">`
			for i := 0; i+len(needle) <= len(channel); i++ {
				if channel[i:i+len(needle)] == needle {
					return true
				}
			}
			return false
		}()
}
