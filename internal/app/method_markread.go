package app

import (
	"context"
	"encoding/json"

	"github.com/yolo-labz/wa/internal/domain"
)

// markReadParams is the JSON-RPC params for the "markRead" method (FR-008).
type markReadParams struct {
	Chat      string `json:"chat"`
	MessageID string `json:"messageId"`
}

// handleMarkRead implements the "markRead" JSON-RPC method (FR-008, FR-009).
//
// It runs the safety pipeline (allowlist + rate limiter) before calling
// MessageSender.MarkRead, then records an audit entry.
func (d *Dispatcher) handleMarkRead(ctx context.Context, raw json.RawMessage) (json.RawMessage, error) {
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

	if err := d.sender.MarkRead(ctx, jid, domain.MessageID(p.MessageID)); err != nil {
		d.recordAudit(ctx, jid, "error", err.Error())
		return nil, err
	}

	d.recordAudit(ctx, jid, "ok", "")

	return json.Marshal(struct{}{})
}
