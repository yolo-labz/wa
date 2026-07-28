package app

import (
	"context"
	"encoding/json"

	"github.com/yolo-labz/wa/v2/internal/domain"
)

// markReadParams is the JSON-RPC params for the "markRead" method (FR-008).
type markReadParams struct {
	Chat      string `json:"chat"`
	MessageID string `json:"messageId"`
	// Played upgrades the receipt to whatsmeow's "played" type, which is
	// what tells the sender their voice note was listened to (or their
	// view-once media opened) rather than merely read. It rides markRead
	// instead of getting its own method because the caller intent is one
	// thing — acknowledge this message — with a stronger and a weaker
	// form; the port keeps them as two methods so an adapter cannot
	// silently downgrade one to the other.
	Played bool `json:"played"`
}

// handleMarkRead implements the "markRead" JSON-RPC method (FR-008, FR-009).
//
// It runs the safety pipeline (allowlist + rate limiter) before calling
// MessageSender.MarkRead, then records an audit entry. Wrapped in the
// FR-034a idempotency sidecar.
func (d *Dispatcher) handleMarkRead(ctx context.Context, raw json.RawMessage) (json.RawMessage, error) {
	return d.idempotentCall(ctx, "markRead", raw, func(ctx context.Context) (json.RawMessage, error) {
		return d.doMarkRead(ctx, raw)
	})
}

func (d *Dispatcher) doMarkRead(ctx context.Context, raw json.RawMessage) (json.RawMessage, error) {
	var p markReadParams
	if err := parseParams(raw, &p); err != nil {
		return nil, err
	}
	if p.Chat == "" || p.MessageID == "" {
		return nil, ErrInvalidParams
	}

	jid, err := domain.Parse(p.Chat)
	if err != nil {
		return nil, ErrInvalidJID
	}

	// Safety pipeline: allowlist + rate limiter (FR-009).
	if err := d.checkSafetyAndAudit(ctx, jid, domain.ActionRead); err != nil {
		return nil, err
	}

	mark := d.sender.MarkRead
	if p.Played {
		mark = d.sender.MarkPlayed
	}
	if err := mark(ctx, jid, domain.MessageID(p.MessageID)); err != nil {
		d.recordAudit(ctx, jid, "error", auditErrDetail(err))
		return nil, err
	}

	d.recordAudit(ctx, jid, "ok", "")

	return json.Marshal(struct{}{})
}
