package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/yolo-labz/wa/internal/domain"
)

// revokeParams is the JSON-RPC params for "message.revoke" (FR-014).
// Scope defaults to "everyone" when omitted to match the common case:
// the caller is retracting their own message from the chat.
type revokeParams struct {
	Chat      string `json:"chat"`
	MessageID string `json:"messageId"`
	Scope     string `json:"scope,omitempty"`
}

// editParams is the JSON-RPC params for "message.edit" (FR-015, FR-015a).
type editParams struct {
	Chat      string `json:"chat"`
	MessageID string `json:"messageId"`
	Body      string `json:"body"`
}

// handleMessageRevoke implements "message.revoke" (FR-014). Wrapped in the
// FR-034a idempotency sidecar — retries under the same idempotencyKey
// replay the cached `{}` result rather than firing a second tombstone.
func (d *Dispatcher) handleMessageRevoke(ctx context.Context, raw json.RawMessage) (json.RawMessage, error) {
	return d.idempotentCall(ctx, "message.revoke", raw, func(ctx context.Context) (json.RawMessage, error) {
		return d.doMessageRevoke(ctx, raw)
	})
}

func (d *Dispatcher) doMessageRevoke(ctx context.Context, raw json.RawMessage) (json.RawMessage, error) {
	if d.moderator == nil {
		return nil, ErrMethodNotFound
	}
	var p revokeParams
	if err := parseParams(raw, &p); err != nil {
		return nil, err
	}
	if p.Chat == "" || p.MessageID == "" {
		return nil, ErrInvalidParams
	}
	scopeStr := p.Scope
	if scopeStr == "" {
		scopeStr = "everyone"
	}
	scope, err := domain.ParseRevokeScope(scopeStr)
	if err != nil {
		return nil, ErrInvalidParams
	}
	jid, err := domain.Parse(p.Chat)
	if err != nil {
		return nil, ErrInvalidJID
	}
	if err := d.checkSafetyAndAudit(ctx, jid, domain.ActionRevoke); err != nil {
		return nil, err
	}
	if err := d.moderator.Revoke(ctx, jid, domain.MessageID(p.MessageID), scope); err != nil {
		d.recordModerationAudit(ctx, domain.AuditRevoke, jid, "error", err.Error())
		return nil, fmt.Errorf("message.revoke: %w", err)
	}
	d.recordModerationAudit(ctx, domain.AuditRevoke, jid, "ok", p.MessageID+":"+scope.String())
	return json.Marshal(struct{}{})
}

// handleMessageEdit implements "message.edit" (FR-015, FR-015a). Wrapped
// in the FR-034a idempotency sidecar — same key + same body replays the
// cached result, same key + different body yields -32101. The 15-min
// window check happens in the adapter; domain.ErrOutsideEditWindow
// bubbles up and is mapped to -32100 at the socket boundary.
func (d *Dispatcher) handleMessageEdit(ctx context.Context, raw json.RawMessage) (json.RawMessage, error) {
	return d.idempotentCall(ctx, "message.edit", raw, func(ctx context.Context) (json.RawMessage, error) {
		return d.doMessageEdit(ctx, raw)
	})
}

func (d *Dispatcher) doMessageEdit(ctx context.Context, raw json.RawMessage) (json.RawMessage, error) {
	if d.moderator == nil {
		return nil, ErrMethodNotFound
	}
	var p editParams
	if err := parseParams(raw, &p); err != nil {
		return nil, err
	}
	if p.Chat == "" || p.MessageID == "" || p.Body == "" {
		return nil, ErrInvalidParams
	}
	jid, err := domain.Parse(p.Chat)
	if err != nil {
		return nil, ErrInvalidJID
	}
	if err := d.checkSafetyAndAudit(ctx, jid, domain.ActionEdit); err != nil {
		return nil, err
	}
	if err := d.moderator.Edit(ctx, jid, domain.MessageID(p.MessageID), p.Body); err != nil {
		d.recordModerationAudit(ctx, domain.AuditEdit, jid, "error", err.Error())
		// Preserve typed errors (ErrOutsideEditWindow → -32100,
		// ErrMessageTooLarge → -32201). fmt.Errorf wrapping keeps
		// errors.Is working for the socket dispatcher.
		if errors.Is(err, domain.ErrOutsideEditWindow) || errors.Is(err, domain.ErrMessageTooLarge) {
			return nil, err
		}
		return nil, fmt.Errorf("message.edit: %w", err)
	}
	d.recordModerationAudit(ctx, domain.AuditEdit, jid, "ok", p.MessageID)
	return json.Marshal(struct{}{})
}

// recordModerationAudit writes a moderation AuditKind (AuditRevoke /
// AuditEdit) entry. Mirrors recordAudit but with a non-hardcoded action
// so both verbs share the pipeline.
func (d *Dispatcher) recordModerationAudit(ctx context.Context, action domain.AuditAction, subject domain.JID, decision, detail string) {
	evt := domain.NewAuditEvent("dispatcher", action, subject, decision, detail)
	if err := d.audit.Record(ctx, evt); err != nil {
		d.log.Error("audit write failed", "err", err)
	}
}
