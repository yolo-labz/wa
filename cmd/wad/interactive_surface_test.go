package main

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/yolo-labz/wa/v2/internal/adapters/secondary/sqlitehistory"
	"github.com/yolo-labz/wa/v2/internal/domain"
)

// TestStoredToWire_SurfacesInteractive is the issue #201 read-surface proof:
// a StoredMessage carrying a persisted interactive_json BLOB is rendered
// into the client wireMessage with the interactive selection (subtype +
// option id/label) under a stable `interactive` key.
func TestStoredToWire_SurfacesInteractive(t *testing.T) {
	t.Parallel()

	blob, err := sqlitehistory.MarshalInteractive(&domain.InteractivePayload{
		Subtype: domain.InteractiveList,
		Prompt:  "Choose a unit",
		Options: []domain.InteractiveOption{{ID: "unit_recife_riomar", Label: "Unidade Recife - Rio Mar"}},
	})
	if err != nil {
		t.Fatalf("MarshalInteractive: %v", err)
	}

	wire := storedToWire([]sqlitehistory.StoredMessage{{
		ChatJID:         "558134658209@s.whatsapp.net",
		SenderJID:       "558199999999@s.whatsapp.net",
		MessageID:       "MSG-1",
		Body:            "Choose a unit",
		InteractiveJSON: blob,
	}})
	if len(wire) != 1 {
		t.Fatalf("storedToWire returned %d, want 1", len(wire))
	}
	if wire[0].Interactive == nil {
		t.Fatal("wireMessage.Interactive is nil; want populated for interactive reply")
	}
	if wire[0].Interactive.Subtype != "list" {
		t.Errorf("interactive.subtype = %q, want %q", wire[0].Interactive.Subtype, "list")
	}
	if len(wire[0].Interactive.Options) != 1 ||
		wire[0].Interactive.Options[0].ID != "unit_recife_riomar" ||
		wire[0].Interactive.Options[0].Label != "Unidade Recife - Rio Mar" {
		t.Errorf("interactive.options = %+v, want [{unit_recife_riomar Unidade Recife - Rio Mar}]", wire[0].Interactive.Options)
	}

	// Assert the field actually appears in the serialized JSON under the
	// stable `interactive` key (the client-facing contract).
	raw, err := json.Marshal(wire[0])
	if err != nil {
		t.Fatalf("marshal wireMessage: %v", err)
	}
	js := string(raw)
	for _, want := range []string{`"interactive"`, `"subtype":"list"`, `"id":"unit_recife_riomar"`, `"label":"Unidade Recife - Rio Mar"`} {
		if !strings.Contains(js, want) {
			t.Errorf("serialized wireMessage missing %s\ngot: %s", want, js)
		}
	}
}

// TestStoredToWire_OmitsInteractiveForOrdinary confirms ordinary messages
// (no interactive_json) omit the `interactive` key entirely (issue #201).
func TestStoredToWire_OmitsInteractiveForOrdinary(t *testing.T) {
	t.Parallel()
	wire := storedToWire([]sqlitehistory.StoredMessage{{
		ChatJID:   "558134658209@s.whatsapp.net",
		SenderJID: "558134658209@s.whatsapp.net",
		MessageID: "MSG-PLAIN",
		Body:      "hello",
	}})
	if len(wire) != 1 {
		t.Fatalf("storedToWire returned %d, want 1", len(wire))
	}
	if wire[0].Interactive != nil {
		t.Errorf("wireMessage.Interactive = %+v, want nil for ordinary message", wire[0].Interactive)
	}
	raw, err := json.Marshal(wire[0])
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if strings.Contains(string(raw), `"interactive"`) {
		t.Errorf("ordinary message JSON should omit interactive key, got: %s", raw)
	}
}

// TestMessageEvent_LiveSurfaceCarriesInteractive locks the live event
// surface (REST /v1/events SSE marshals domain.MessageEvent verbatim — see
// cmd/wad/rest_http.go restEventStreamAdapter). It guards against a future
// change to MessageEvent that would drop the Interactive field from the
// pushed event JSON (issue #201). This mirrors how the sibling Quoted
// field is surfaced — raw struct marshaling, no DTO.
func TestMessageEvent_LiveSurfaceCarriesInteractive(t *testing.T) {
	t.Parallel()
	ev := domain.MessageEvent{
		ID:       "evt-1",
		PushName: "Cliente",
		Interactive: &domain.InteractivePayload{
			Subtype: domain.InteractiveButtons,
			Prompt:  "Yes or no?",
			Options: []domain.InteractiveOption{{ID: "btn_yes", Label: "Yes"}},
		},
	}
	raw, err := json.Marshal(ev)
	if err != nil {
		t.Fatalf("marshal MessageEvent: %v", err)
	}
	js := string(raw)
	for _, want := range []string{`"Interactive"`, `"btn_yes"`, `"Yes"`} {
		if !strings.Contains(js, want) {
			t.Errorf("live MessageEvent JSON missing %s\ngot: %s", want, js)
		}
	}
}
