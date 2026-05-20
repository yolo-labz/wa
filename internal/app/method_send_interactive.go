package app

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/yolo-labz/wa/v2/internal/domain"
	"github.com/yolo-labz/wa/v2/internal/observability"
)

// sendListResponseParams is the JSON-RPC params for "send.listResponse".
// Spec 110j FR-004 (#161 amendment).
//
// ContextStanzaID is required: the WhatsApp wire rejects reply-class
// interactive sends that do not quote the original via ContextInfo
// (server error 479). ContextSender defaults to To when omitted — for
// 1:1 chats with a business bot the sender of the inbound list IS the
// recipient of our reply.
type sendListResponseParams struct {
	To              string `json:"to"`
	RowID           string `json:"rowId"`
	Title           string `json:"title,omitempty"`
	ContextStanzaID string `json:"contextStanzaId"`
	ContextSender   string `json:"contextSender,omitempty"`
	IdempotencyKey  string `json:"idempotencyKey,omitempty"`
}

// sendButtonResponseParams is the JSON-RPC params for "send.buttonResponse".
// Spec 110j FR-004 (#161 amendment). See sendListResponseParams for
// ContextStanzaID / ContextSender semantics — same rule applies.
type sendButtonResponseParams struct {
	To              string `json:"to"`
	ButtonID        string `json:"buttonId"`
	DisplayText     string `json:"displayText,omitempty"`
	Kind            string `json:"kind,omitempty"`
	ContextStanzaID string `json:"contextStanzaId"`
	ContextSender   string `json:"contextSender,omitempty"`
	IdempotencyKey  string `json:"idempotencyKey,omitempty"`
}

// handleSendListResponse implements "send.listResponse" — reply to a peer's
// interactive ListMessage with the SelectedRowID it offered. Spec 110j.
func (d *Dispatcher) handleSendListResponse(ctx context.Context, raw json.RawMessage) (json.RawMessage, error) {
	return d.idempotentCall(ctx, "send.listResponse", raw, func(ctx context.Context) (json.RawMessage, error) {
		return d.doSendListResponse(ctx, raw)
	})
}

func (d *Dispatcher) doSendListResponse(ctx context.Context, raw json.RawMessage) (json.RawMessage, error) {
	var p sendListResponseParams
	if err := parseParams(raw, &p); err != nil {
		return nil, err
	}
	if p.To == "" || p.RowID == "" || p.ContextStanzaID == "" {
		return nil, ErrInvalidParams
	}

	jid, err := domain.Parse(p.To)
	if err != nil {
		return nil, ErrInvalidJID
	}

	// ContextSender defaults to To: in a 1:1 chat the sender of the
	// inbound interactive is the recipient of our reply.
	senderStr := p.ContextSender
	if senderStr == "" {
		senderStr = p.To
	}
	ctxSender, err := domain.Parse(senderStr)
	if err != nil {
		return nil, ErrInvalidJID
	}

	ctx, span := observability.StartSend(ctx, d.profile, "send.listResponse", p.To)
	defer span.End()

	if err := d.checkSafetyAndAudit(ctx, jid, domain.ActionSend); err != nil {
		return nil, err
	}
	if err := d.ensureNotBlocked(ctx, jid); err != nil {
		d.recordAudit(ctx, jid, "denied:blocked", "")
		return nil, err
	}

	msg := domain.ListReplyMessage{
		Recipient:       jid,
		RowID:           p.RowID,
		Title:           p.Title,
		ContextStanzaID: domain.MessageID(p.ContextStanzaID),
		ContextSender:   ctxSender,
	}
	id, err := d.sender.Send(ctx, msg)
	if err != nil {
		d.recordAudit(ctx, jid, "error", err.Error())
		return nil, fmt.Errorf("send.listResponse: %w", err)
	}

	d.recordAudit(ctx, jid, "ok", "list_response:"+p.RowID)

	return marshalResult(sendResult{
		MessageID: string(id),
		Timestamp: time.Now().Unix(),
	})
}

// handleSendButtonResponse implements "send.buttonResponse" — reply to a
// peer's ButtonsMessage or TemplateMessage with the SelectedButtonID it
// offered. Spec 110j.
func (d *Dispatcher) handleSendButtonResponse(ctx context.Context, raw json.RawMessage) (json.RawMessage, error) {
	return d.idempotentCall(ctx, "send.buttonResponse", raw, func(ctx context.Context) (json.RawMessage, error) {
		return d.doSendButtonResponse(ctx, raw)
	})
}

func (d *Dispatcher) doSendButtonResponse(ctx context.Context, raw json.RawMessage) (json.RawMessage, error) {
	var p sendButtonResponseParams
	if err := parseParams(raw, &p); err != nil {
		return nil, err
	}
	if p.To == "" || p.ButtonID == "" || p.ContextStanzaID == "" {
		return nil, ErrInvalidParams
	}

	var kind domain.ButtonReplyKind
	switch p.Kind {
	case "", "button":
		kind = domain.ButtonReplyButtons
	case "templateButton":
		kind = domain.ButtonReplyTemplate
	default:
		return nil, ErrInvalidParams
	}

	jid, err := domain.Parse(p.To)
	if err != nil {
		return nil, ErrInvalidJID
	}

	senderStr := p.ContextSender
	if senderStr == "" {
		senderStr = p.To
	}
	ctxSender, err := domain.Parse(senderStr)
	if err != nil {
		return nil, ErrInvalidJID
	}

	ctx, span := observability.StartSend(ctx, d.profile, "send.buttonResponse", p.To)
	defer span.End()

	if err := d.checkSafetyAndAudit(ctx, jid, domain.ActionSend); err != nil {
		return nil, err
	}
	if err := d.ensureNotBlocked(ctx, jid); err != nil {
		d.recordAudit(ctx, jid, "denied:blocked", "")
		return nil, err
	}

	msg := domain.ButtonReplyMessage{
		Recipient:       jid,
		ButtonID:        p.ButtonID,
		DisplayText:     p.DisplayText,
		Kind:            kind,
		ContextStanzaID: domain.MessageID(p.ContextStanzaID),
		ContextSender:   ctxSender,
	}
	id, err := d.sender.Send(ctx, msg)
	if err != nil {
		d.recordAudit(ctx, jid, "error", err.Error())
		return nil, fmt.Errorf("send.buttonResponse: %w", err)
	}

	d.recordAudit(ctx, jid, "ok", "button_response:"+p.ButtonID)

	return marshalResult(sendResult{
		MessageID: string(id),
		Timestamp: time.Now().Unix(),
	})
}
