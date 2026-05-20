package whatsmeow

import (
	"testing"

	"github.com/yolo-labz/wa/v2/internal/domain"
)

// TestRoundTrip_ListReply — spec 110j: outbound build → inbound translate
// preserves the SelectedRowID. Catches proto-field-name drift across
// whatsmeow bumps.
func TestRoundTrip_ListReply(t *testing.T) {
	t.Parallel()
	chat := domain.MustJID("558134658209@s.whatsapp.net")
	msg := domain.ListReplyMessage{
		Recipient: chat,
		RowID:     "row-7",
		Title:     "Unidade Recife - Rio Mar",
	}
	wire := buildListResponseMessage(msg, chat)
	payload := extractInteractive(wire)
	if payload == nil {
		t.Fatal("extractInteractive returned nil")
	}
	if payload.Subtype != domain.InteractiveList {
		t.Errorf("subtype = %v, want InteractiveList", payload.Subtype)
	}
	if len(payload.Options) != 1 {
		t.Fatalf("options = %d, want 1", len(payload.Options))
	}
	if got := payload.Options[0].ID; got != "row-7" {
		t.Errorf("option.ID = %q, want %q", got, "row-7")
	}
}

// TestRoundTrip_ButtonReply — spec 110j: outbound buttons build → inbound
// translate preserves the SelectedButtonID + display text.
func TestRoundTrip_ButtonReply(t *testing.T) {
	t.Parallel()
	chat := domain.MustJID("558134658209@s.whatsapp.net")
	msg := domain.ButtonReplyMessage{
		Recipient:   chat,
		ButtonID:    "btn-yes",
		DisplayText: "Sim",
		Kind:        domain.ButtonReplyButtons,
	}
	wire := buildButtonReplyMessage(msg, chat)
	payload := extractInteractive(wire)
	if payload == nil {
		t.Fatal("extractInteractive returned nil")
	}
	if payload.Subtype != domain.InteractiveButtons {
		t.Errorf("subtype = %v, want InteractiveButtons", payload.Subtype)
	}
	if len(payload.Options) != 1 {
		t.Fatalf("options = %d, want 1", len(payload.Options))
	}
	if got := payload.Options[0].ID; got != "btn-yes" {
		t.Errorf("option.ID = %q, want %q", got, "btn-yes")
	}
	if got := payload.Options[0].Label; got != "Sim" {
		t.Errorf("option.Label = %q, want %q", got, "Sim")
	}
}

// TestRoundTrip_TemplateButtonReply — spec 110j: outbound template build →
// inbound translate preserves the SelectedID + display text.
func TestRoundTrip_TemplateButtonReply(t *testing.T) {
	t.Parallel()
	chat := domain.MustJID("558134658209@s.whatsapp.net")
	msg := domain.ButtonReplyMessage{
		Recipient:   chat,
		ButtonID:    "tpl-7",
		DisplayText: "Atendente",
		Kind:        domain.ButtonReplyTemplate,
	}
	wire := buildButtonReplyMessage(msg, chat)
	payload := extractInteractive(wire)
	if payload == nil {
		t.Fatal("extractInteractive returned nil")
	}
	if payload.Subtype != domain.InteractiveButtons {
		t.Errorf("subtype = %v, want InteractiveButtons", payload.Subtype)
	}
	if len(payload.Options) != 1 {
		t.Fatalf("options = %d, want 1", len(payload.Options))
	}
	if got := payload.Options[0].ID; got != "tpl-7" {
		t.Errorf("option.ID = %q, want %q", got, "tpl-7")
	}
	if got := payload.Options[0].Label; got != "Atendente" {
		t.Errorf("option.Label = %q, want %q", got, "Atendente")
	}
}
