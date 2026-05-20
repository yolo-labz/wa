package app_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/yolo-labz/wa/v2/internal/app"
	"github.com/yolo-labz/wa/v2/internal/domain"
)

// TestSendListResponse_Happy — spec 110j FR-004: send.listResponse routes
// through the safety pipeline and returns a messageId.
func TestSendListResponse_Happy(t *testing.T) {
	d, adapter := newTestDispatcher(t, 30*24*time.Hour)
	jid := domain.MustJID(testJIDStr)
	adapter.Grant(jid, domain.ActionSend)

	params, _ := json.Marshal(map[string]string{
		"to":              testJIDStr,
		"rowId":           "row-7",
		"title":           "Atendente",
		"contextStanzaId": "orig-stanza-1",
	})
	result, err := d.Handle(context.Background(), "send.listResponse", params)
	if err != nil {
		t.Fatalf("Handle(send.listResponse): %v", err)
	}

	var res struct {
		MessageID string `json:"messageId"`
		Timestamp int64  `json:"timestamp"`
	}
	if err := json.Unmarshal(result, &res); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if res.MessageID == "" {
		t.Error("empty messageId")
	}

	sent := adapter.Sent()
	if len(sent) != 1 {
		t.Fatalf("sent = %d, want 1", len(sent))
	}
	lr, ok := sent[0].(domain.ListReplyMessage)
	if !ok {
		t.Fatalf("wrong message variant: %T", sent[0])
	}
	if lr.RowID != "row-7" || lr.Title != "Atendente" {
		t.Errorf("got %+v", lr)
	}

	entries := adapter.AuditEntries()
	if len(entries) != 1 {
		t.Fatalf("audit = %d, want 1", len(entries))
	}
	if entries[0].Detail != "list_response:row-7" {
		t.Errorf("audit detail = %q, want list_response:row-7", entries[0].Detail)
	}
}

// TestSendListResponse_DeniedByAllowlist — spec 110j FR-004: same allowlist
// pipeline as send; non-allowlisted JID → ErrNotAllowlisted (-32100).
func TestSendListResponse_DeniedByAllowlist(t *testing.T) {
	d, adapter := newTestDispatcher(t, 30*24*time.Hour)
	// no Grant

	params, _ := json.Marshal(map[string]string{
		"to":              testJIDStr,
		"rowId":           "row-7",
		"contextStanzaId": "orig-stanza-1",
	})
	_, err := d.Handle(context.Background(), "send.listResponse", params)
	if !errors.Is(err, app.ErrNotAllowlisted) {
		t.Fatalf("err = %v, want ErrNotAllowlisted", err)
	}
	if len(adapter.Sent()) != 0 {
		t.Error("must not send when allowlist denies")
	}
}

// TestSendListResponse_MissingRowID — spec 110j FR-004: missing rowId →
// ErrInvalidParams (-32602).
func TestSendListResponse_MissingRowID(t *testing.T) {
	d, adapter := newTestDispatcher(t, 30*24*time.Hour)
	adapter.Grant(domain.MustJID(testJIDStr), domain.ActionSend)

	params, _ := json.Marshal(map[string]string{
		"to":              testJIDStr,
		"contextStanzaId": "orig-stanza-1",
	})
	_, err := d.Handle(context.Background(), "send.listResponse", params)
	if !errors.Is(err, app.ErrInvalidParams) {
		t.Errorf("err = %v, want ErrInvalidParams", err)
	}
}

// TestSendListResponse_MissingContextStanzaID — spec 110j FR-004 (#161): the
// daemon rejects a list response without contextStanzaId at the params
// boundary instead of letting the WhatsApp server return error 479.
func TestSendListResponse_MissingContextStanzaID(t *testing.T) {
	d, adapter := newTestDispatcher(t, 30*24*time.Hour)
	adapter.Grant(domain.MustJID(testJIDStr), domain.ActionSend)

	params, _ := json.Marshal(map[string]string{
		"to":    testJIDStr,
		"rowId": "row-7",
	})
	_, err := d.Handle(context.Background(), "send.listResponse", params)
	if !errors.Is(err, app.ErrInvalidParams) {
		t.Errorf("err = %v, want ErrInvalidParams", err)
	}
	if len(adapter.Sent()) != 0 {
		t.Error("must not reach the adapter without contextStanzaId")
	}
}

// TestSendButtonResponse_MissingContextStanzaID — spec 110j FR-004 (#161):
// same boundary check as the list variant.
func TestSendButtonResponse_MissingContextStanzaID(t *testing.T) {
	d, adapter := newTestDispatcher(t, 30*24*time.Hour)
	adapter.Grant(domain.MustJID(testJIDStr), domain.ActionSend)

	params, _ := json.Marshal(map[string]string{
		"to":       testJIDStr,
		"buttonId": "btn-yes",
	})
	_, err := d.Handle(context.Background(), "send.buttonResponse", params)
	if !errors.Is(err, app.ErrInvalidParams) {
		t.Errorf("err = %v, want ErrInvalidParams", err)
	}
	if len(adapter.Sent()) != 0 {
		t.Error("must not reach the adapter without contextStanzaId")
	}
}

// TestSendListResponse_ContextSenderDefaultsToTo — #161: in a 1:1 chat the
// sender of the inbound list IS the recipient of our reply, so the
// dispatcher MUST default ContextSender to To when contextSender is
// omitted. Asserts via the domain message that reaches the adapter.
func TestSendListResponse_ContextSenderDefaultsToTo(t *testing.T) {
	d, adapter := newTestDispatcher(t, 30*24*time.Hour)
	jid := domain.MustJID(testJIDStr)
	adapter.Grant(jid, domain.ActionSend)

	params, _ := json.Marshal(map[string]string{
		"to":              testJIDStr,
		"rowId":           "row-7",
		"contextStanzaId": "orig-stanza-1",
	})
	if _, err := d.Handle(context.Background(), "send.listResponse", params); err != nil {
		t.Fatalf("Handle: %v", err)
	}
	lr := adapter.Sent()[0].(domain.ListReplyMessage)
	if lr.ContextSender != jid {
		t.Errorf("ContextSender = %v, want %v (defaulted from To)", lr.ContextSender, jid)
	}
	if string(lr.ContextStanzaID) != "orig-stanza-1" {
		t.Errorf("ContextStanzaID = %q, want %q", lr.ContextStanzaID, "orig-stanza-1")
	}
}

// TestSendButtonResponse_Happy_Buttons — spec 110j FR-004: kind="button"
// (default) reaches the adapter as ButtonReplyButtons.
func TestSendButtonResponse_Happy_Buttons(t *testing.T) {
	d, adapter := newTestDispatcher(t, 30*24*time.Hour)
	jid := domain.MustJID(testJIDStr)
	adapter.Grant(jid, domain.ActionSend)

	params, _ := json.Marshal(map[string]string{
		"to":              testJIDStr,
		"buttonId":        "btn-yes",
		"displayText":     "Yes",
		"contextStanzaId": "orig-stanza-1",
	})
	result, err := d.Handle(context.Background(), "send.buttonResponse", params)
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}
	var res struct {
		MessageID string `json:"messageId"`
	}
	if err := json.Unmarshal(result, &res); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if res.MessageID == "" {
		t.Error("empty messageId")
	}

	br, ok := adapter.Sent()[0].(domain.ButtonReplyMessage)
	if !ok {
		t.Fatalf("wrong variant: %T", adapter.Sent()[0])
	}
	if br.Kind != domain.ButtonReplyButtons {
		t.Errorf("kind = %v, want Buttons", br.Kind)
	}
	if br.ButtonID != "btn-yes" || br.DisplayText != "Yes" {
		t.Errorf("got %+v", br)
	}

	entries := adapter.AuditEntries()
	if entries[0].Detail != "button_response:btn-yes" {
		t.Errorf("audit detail = %q", entries[0].Detail)
	}
}

// TestSendButtonResponse_Happy_Template — spec 110j FR-004: kind="templateButton"
// reaches the adapter as ButtonReplyTemplate.
func TestSendButtonResponse_Happy_Template(t *testing.T) {
	d, adapter := newTestDispatcher(t, 30*24*time.Hour)
	jid := domain.MustJID(testJIDStr)
	adapter.Grant(jid, domain.ActionSend)

	params, _ := json.Marshal(map[string]string{
		"to":              testJIDStr,
		"buttonId":        "tpl-1",
		"kind":            "templateButton",
		"contextStanzaId": "orig-stanza-1",
	})
	_, err := d.Handle(context.Background(), "send.buttonResponse", params)
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}

	br := adapter.Sent()[0].(domain.ButtonReplyMessage)
	if br.Kind != domain.ButtonReplyTemplate {
		t.Errorf("kind = %v, want Template", br.Kind)
	}
}

// TestSendButtonResponse_InvalidKind — spec 110j FR-004: kind outside the
// closed enum → ErrInvalidParams.
func TestSendButtonResponse_InvalidKind(t *testing.T) {
	d, adapter := newTestDispatcher(t, 30*24*time.Hour)
	adapter.Grant(domain.MustJID(testJIDStr), domain.ActionSend)

	params, _ := json.Marshal(map[string]string{
		"to":              testJIDStr,
		"buttonId":        "btn-1",
		"kind":            "rocketShip",
		"contextStanzaId": "orig-stanza-1",
	})
	_, err := d.Handle(context.Background(), "send.buttonResponse", params)
	if !errors.Is(err, app.ErrInvalidParams) {
		t.Errorf("err = %v, want ErrInvalidParams", err)
	}
}

// TestSendButtonResponse_MissingButtonID — spec 110j FR-004: missing buttonId
// → ErrInvalidParams.
func TestSendButtonResponse_MissingButtonID(t *testing.T) {
	d, adapter := newTestDispatcher(t, 30*24*time.Hour)
	adapter.Grant(domain.MustJID(testJIDStr), domain.ActionSend)

	params, _ := json.Marshal(map[string]string{"to": testJIDStr})
	_, err := d.Handle(context.Background(), "send.buttonResponse", params)
	if !errors.Is(err, app.ErrInvalidParams) {
		t.Errorf("err = %v, want ErrInvalidParams", err)
	}
}
