package app

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/yolo-labz/wa/v2/internal/domain"
)

func transcribedFixture() domain.MediaTranscribedEvent {
	var sum [32]byte
	for i := range sum {
		sum[i] = byte(i)
	}
	return domain.MediaTranscribedEvent{
		ID:        "evt-9",
		TS:        time.Unix(1781000000, 0),
		SHA256:    sum,
		MessageID: "3EB0AAAA",
		Lang:      "pt",
		Chars:     21,
		Adapter:   "whispercpp",
	}
}

// TestMediaTranscribedWireShape pins the keys spec 110h documents.
//
// The domain struct was marshalled verbatim and carries no JSON tags, so
// the wire keys were the Go field names ("ID", "SHA256", "MessageID")
// while every other subscriber payload is lower-camel — a consumer
// dispatching on messageId across kinds silently missed this one.
func TestMediaTranscribedWireShape(t *testing.T) {
	t.Parallel()
	raw, err := json.Marshal(wrapMediaTranscribedForSubscribers(transcribedFixture()))
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var wire map[string]any
	if err := json.Unmarshal(raw, &wire); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	want := map[string]any{
		"id":        "evt-9",
		"ts":        float64(1781000000),
		"sha256":    "000102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f",
		"messageId": "3EB0AAAA",
		"lang":      "pt",
		"chars":     float64(21),
		"adapter":   "whispercpp",
	}
	for k, v := range want {
		if wire[k] != v {
			t.Errorf("%s = %#v, want %#v", k, wire[k], v)
		}
	}

	// The Go field names must be gone, not merely accompanied.
	for _, gone := range []string{"ID", "TS", "SHA256", "MessageID", "Lang", "Chars", "Adapter"} {
		if _, present := wire[gone]; present {
			t.Errorf("Go field name %q still on the wire: %s", gone, raw)
		}
	}
}

// TestMediaTranscribedSHA256IsHexNotArray — [32]byte marshals as a
// 32-element JSON array. Spec 110h documents a hex string, and hex is
// what a caller pastes back into media.fetchBytes; an array is unusable
// there without client-side reassembly.
func TestMediaTranscribedSHA256IsHexNotArray(t *testing.T) {
	t.Parallel()
	raw, err := json.Marshal(wrapMediaTranscribedForSubscribers(transcribedFixture()))
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var wire struct {
		SHA256 any `json:"sha256"`
	}
	if err := json.Unmarshal(raw, &wire); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	s, ok := wire.SHA256.(string)
	if !ok {
		t.Fatalf("sha256 is %T, want a hex string (the [32]byte leaked as an array)", wire.SHA256)
	}
	if len(s) != 64 {
		t.Errorf("sha256 = %q (%d chars), want 64 hex chars", s, len(s))
	}
}

// TestMediaTranscribedGoesThroughTheBridge — the projection is only worth
// anything if translateDomainEvent actually uses it. Emitting the domain
// struct verbatim was the bug; a projection nothing calls would leave it.
func TestMediaTranscribedGoesThroughTheBridge(t *testing.T) {
	t.Parallel()
	got := translateDomainEvent(transcribedFixture())
	if got.Type != "media.transcribed" {
		t.Fatalf("Type = %q, want media.transcribed", got.Type)
	}
	if _, ok := got.Payload.(SubscriberMediaTranscribedEvent); !ok {
		t.Fatalf("Payload is %T, want SubscriberMediaTranscribedEvent — the bridge still emits the domain struct", got.Payload)
	}
}

// TestMediaTranscribedOmitsEmptyOptionals — lang and adapter are
// best-effort metadata; an adapter that reports neither must not put
// empty strings on the wire.
func TestMediaTranscribedOmitsEmptyOptionals(t *testing.T) {
	t.Parallel()
	e := transcribedFixture()
	e.Lang, e.Adapter = "", ""

	raw, err := json.Marshal(wrapMediaTranscribedForSubscribers(e))
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var wire map[string]any
	if err := json.Unmarshal(raw, &wire); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	for _, k := range []string{"lang", "adapter"} {
		if _, present := wire[k]; present {
			t.Errorf("%s should be omitted when empty: %s", k, raw)
		}
	}
	// chars stays even at zero — "the transcript was empty" is a fact,
	// not an absence.
	if _, present := wire["chars"]; !present {
		t.Errorf("chars must always be present: %s", raw)
	}
}
