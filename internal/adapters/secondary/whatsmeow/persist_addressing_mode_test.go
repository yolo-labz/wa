package whatsmeow

import (
	"testing"
	"time"

	waE2E "go.mau.fi/whatsmeow/proto/waE2E"

	waTypes "go.mau.fi/whatsmeow/types"
	waEvents "go.mau.fi/whatsmeow/types/events"
)

// TestPersistInbound_PropagatesAddressingMode pins spec 107: every
// inbound *events.Message MUST forward MessageInfo.AddressingMode +
// MessageInfo.SenderAlt to InsertRaw, where the v5 schema stores them
// for later retrieval. Pre-spec the adapter dropped both fields and
// the agent surface had no way to render the alternate-namespace JID.
func TestPersistInbound_PropagatesAddressingMode(t *testing.T) {
	t.Parallel()
	fc := newFakeClient()
	a := newTestAdapter(t, fc)
	hist := &auditHistoryContainer{}
	a.history = hist
	t.Cleanup(func() { _ = a.Close() })

	chat, _ := waTypes.ParseJID("66448177246461@lid")
	sender, _ := waTypes.ParseJID("66448177246461@lid")
	senderAlt, _ := waTypes.ParseJID("5511999999999@s.whatsapp.net")

	evt := &waEvents.Message{
		Info: waTypes.MessageInfo{
			MessageSource: waTypes.MessageSource{
				Chat:           chat,
				Sender:         sender,
				SenderAlt:      senderAlt,
				AddressingMode: waTypes.AddressingModeLID,
			},
			ID:        "MSG-LID-1",
			Timestamp: time.Unix(1_700_000_000, 0),
			PushName:  "Ricardo",
		},
		Message: &waE2E.Message{Conversation: new("hi")},
	}

	a.persistInboundMessage(evt)

	if len(hist.rawCalls) != 1 {
		t.Fatalf("rawCalls = %d, want 1", len(hist.rawCalls))
	}
	got := hist.rawCalls[0]
	if got.MessageID != "MSG-LID-1" {
		t.Errorf("MessageID = %q, want MSG-LID-1", got.MessageID)
	}
	if got.SenderAltJID != "5511999999999@s.whatsapp.net" {
		t.Errorf("SenderAltJID = %q, want 5511999999999@s.whatsapp.net", got.SenderAltJID)
	}
	if got.AddressingMode != "lid" {
		t.Errorf("AddressingMode = %q, want lid", got.AddressingMode)
	}
}

// TestPersistInbound_EmptyAltAndModeWhenAbsent pins spec 107 FR-002
// semantics: when MessageInfo carries neither SenderAlt nor an
// addressing mode (legacy event payloads or freshly-discovered
// contacts whatsmeow has not yet mapped), InsertRaw receives empty
// strings, which the v5 schema stores as NULL.
func TestPersistInbound_EmptyAltAndModeWhenAbsent(t *testing.T) {
	t.Parallel()
	fc := newFakeClient()
	a := newTestAdapter(t, fc)
	hist := &auditHistoryContainer{}
	a.history = hist
	t.Cleanup(func() { _ = a.Close() })

	chat, _ := waTypes.ParseJID("5511999999999@s.whatsapp.net")
	evt := &waEvents.Message{
		Info: waTypes.MessageInfo{
			MessageSource: waTypes.MessageSource{Chat: chat, Sender: chat},
			ID:            "MSG-LEGACY-1",
			Timestamp:     time.Unix(1_700_000_001, 0),
		},
		Message: &waE2E.Message{Conversation: new("legacy")},
	}

	a.persistInboundMessage(evt)

	if len(hist.rawCalls) != 1 {
		t.Fatalf("rawCalls = %d, want 1", len(hist.rawCalls))
	}
	got := hist.rawCalls[0]
	if got.SenderAltJID != "" {
		t.Errorf("SenderAltJID = %q, want empty (legacy event)", got.SenderAltJID)
	}
	if got.AddressingMode != "" {
		t.Errorf("AddressingMode = %q, want empty (legacy event)", got.AddressingMode)
	}
}
