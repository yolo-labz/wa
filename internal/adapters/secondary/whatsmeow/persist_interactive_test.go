package whatsmeow

import (
	"encoding/json"
	"testing"
	"time"

	waE2E "go.mau.fi/whatsmeow/proto/waE2E"
	waTypes "go.mau.fi/whatsmeow/types"
	waEvents "go.mau.fi/whatsmeow/types/events"
)

// TestPersistInbound_NativeFlowInteractive is the issue #201 end-to-end
// persist proof: an inbound native-flow interactive reply flows through
// persistInboundMessage → InsertRawInteractive carrying the JSON-encoded
// selection (closing the interactive_json = NULL gap). It mirrors the
// spec-107 addressing-mode persist test's harness.
func TestPersistInbound_NativeFlowInteractive(t *testing.T) {
	t.Parallel()
	fc := newFakeClient()
	a := newTestAdapter(t, fc)
	hist := &auditHistoryContainer{}
	a.history = hist
	t.Cleanup(func() { _ = a.Close() })

	chat, _ := waTypes.ParseJID("558134658209@s.whatsapp.net")
	sender, _ := waTypes.ParseJID("558199999999@s.whatsapp.net")

	evt := &waEvents.Message{
		Info: waTypes.MessageInfo{
			MessageSource: waTypes.MessageSource{Chat: chat, Sender: sender},
			ID:            "MSG-NF-1",
			Timestamp:     time.Unix(1_700_000_000, 0),
			PushName:      "Cliente",
		},
		Message: nativeFlowMsg("single_select",
			`{"id":"unit_recife_riomar","description":"Unidade Recife - Rio Mar"}`,
			"Choose a unit"),
	}

	a.persistInboundMessage(evt)

	if len(hist.rawCalls) != 1 {
		t.Fatalf("rawCalls = %d, want 1", len(hist.rawCalls))
	}
	got := hist.rawCalls[0]
	if got.MessageID != "MSG-NF-1" {
		t.Errorf("MessageID = %q, want MSG-NF-1", got.MessageID)
	}
	if len(got.InteractiveJSON) == 0 {
		t.Fatal("InteractiveJSON is empty; want the encoded native-flow selection")
	}

	// The persisted bytes use the documented wire shape.
	var decoded struct {
		Subtype string `json:"subtype"`
		Prompt  string `json:"prompt"`
		Options []struct {
			ID    string `json:"id"`
			Label string `json:"label"`
		} `json:"options"`
	}
	if err := json.Unmarshal(got.InteractiveJSON, &decoded); err != nil {
		t.Fatalf("unmarshal InteractiveJSON: %v", err)
	}
	if decoded.Subtype != "list" {
		t.Errorf("subtype = %q, want list", decoded.Subtype)
	}
	if decoded.Prompt != "Choose a unit" {
		t.Errorf("prompt = %q, want %q", decoded.Prompt, "Choose a unit")
	}
	if len(decoded.Options) != 1 || decoded.Options[0].ID != "unit_recife_riomar" ||
		decoded.Options[0].Label != "Unidade Recife - Rio Mar" {
		t.Errorf("options = %+v, want one {unit_recife_riomar, Unidade Recife - Rio Mar}", decoded.Options)
	}
}

// TestPersistInbound_PlainMessageNoInteractive confirms an ordinary inbound
// message persists with a nil InteractiveJSON (issue #201) — the column
// must store NULL, not an empty-object payload.
func TestPersistInbound_PlainMessageNoInteractive(t *testing.T) {
	t.Parallel()
	fc := newFakeClient()
	a := newTestAdapter(t, fc)
	hist := &auditHistoryContainer{}
	a.history = hist
	t.Cleanup(func() { _ = a.Close() })

	chat, _ := waTypes.ParseJID("558134658209@s.whatsapp.net")
	evt := &waEvents.Message{
		Info: waTypes.MessageInfo{
			MessageSource: waTypes.MessageSource{Chat: chat, Sender: chat},
			ID:            "MSG-PLAIN-1",
			Timestamp:     time.Unix(1_700_000_001, 0),
		},
		Message: &waE2E.Message{Conversation: new("hi")},
	}

	a.persistInboundMessage(evt)

	if len(hist.rawCalls) != 1 {
		t.Fatalf("rawCalls = %d, want 1", len(hist.rawCalls))
	}
	if got := hist.rawCalls[0].InteractiveJSON; len(got) != 0 {
		t.Errorf("InteractiveJSON = %q, want nil/empty for plain message", got)
	}
}
