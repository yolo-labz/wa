package app

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/yolo-labz/wa/internal/domain"
)

// sendForwardParams is the JSON-RPC params for "message.forward" (FR-033).
type sendForwardParams struct {
	To         string `json:"to"`
	SourceChat string `json:"sourceChat"`
	MessageID  string `json:"messageId"`
}

// sendStarParams is the JSON-RPC params for "message.star" (FR-033).
// Starred false unstars; the wire collapses both verbs onto a boolean.
type sendStarParams struct {
	Chat      string `json:"chat"`
	MessageID string `json:"messageId"`
	Starred   bool   `json:"starred"`
}

// setDisappearingParams is the JSON-RPC params for "message.setDisappearing"
// (FR-033, FR-016). Seconds must be one of {0, 86400, 604800, 7776000}.
type setDisappearingParams struct {
	Chat    string `json:"chat"`
	Seconds int    `json:"seconds"`
}

// legalDisappearing is the 4-value enum WhatsApp exposes for chat-level
// ephemeral timers (off / 24h / 7d / 90d).
var legalDisappearing = map[int]struct{}{
	0:         {},
	86_400:    {},
	604_800:   {},
	7_776_000: {},
}

// handleSendForward implements "message.forward" (FR-033). Requires the
// sender to satisfy ForwardSender; returns ErrMethodNotFound otherwise.
// Wrapped in the FR-034a idempotency sidecar.
func (d *Dispatcher) handleSendForward(ctx context.Context, raw json.RawMessage) (json.RawMessage, error) {
	return d.idempotentCall(ctx, "message.forward", raw, func(ctx context.Context) (json.RawMessage, error) {
		return d.doSendForward(ctx, raw)
	})
}

func (d *Dispatcher) doSendForward(ctx context.Context, raw json.RawMessage) (json.RawMessage, error) {
	fs, ok := d.sender.(ForwardSender)
	if !ok {
		return nil, ErrMethodNotFound
	}
	var p sendForwardParams
	if err := parseParams(raw, &p); err != nil {
		return nil, err
	}
	if p.To == "" || p.SourceChat == "" || p.MessageID == "" {
		return nil, ErrInvalidParams
	}
	toJID, err := domain.Parse(p.To)
	if err != nil {
		return nil, ErrInvalidJID
	}
	srcJID, err := domain.Parse(p.SourceChat)
	if err != nil {
		return nil, ErrInvalidJID
	}
	if err := d.checkSafetyAndAudit(ctx, toJID, domain.ActionSend); err != nil {
		return nil, err
	}
	if err := d.ensureNotBlocked(ctx, toJID); err != nil {
		d.recordAudit(ctx, toJID, "denied:blocked", "")
		return nil, err
	}
	id, err := fs.SendForward(ctx, toJID, srcJID, domain.MessageID(p.MessageID))
	if err != nil {
		d.recordAudit(ctx, toJID, "error", err.Error())
		return nil, fmt.Errorf("message.forward: %w", err)
	}
	d.recordAudit(ctx, toJID, "ok", string(id))
	return marshalResult(sendResult{
		MessageID: string(id),
		Timestamp: time.Now().Unix(),
	})
}

// handleSendStar implements "message.star" (FR-033). Idempotent by spec at
// the adapter layer; the idempotency sidecar guards against duplicate
// retries over the same key.
func (d *Dispatcher) handleSendStar(ctx context.Context, raw json.RawMessage) (json.RawMessage, error) {
	return d.idempotentCall(ctx, "message.star", raw, func(ctx context.Context) (json.RawMessage, error) {
		return d.doSendStar(ctx, raw)
	})
}

func (d *Dispatcher) doSendStar(ctx context.Context, raw json.RawMessage) (json.RawMessage, error) {
	ss, ok := d.sender.(StarSender)
	if !ok {
		return nil, ErrMethodNotFound
	}
	var p sendStarParams
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
	if err := d.checkSafetyAndAudit(ctx, jid, domain.ActionSend); err != nil {
		return nil, err
	}
	if err := ss.Star(ctx, jid, domain.MessageID(p.MessageID), p.Starred); err != nil {
		d.recordAudit(ctx, jid, "error", err.Error())
		return nil, fmt.Errorf("message.star: %w", err)
	}
	d.recordAudit(ctx, jid, "ok", string(p.MessageID))
	return json.Marshal(struct{}{})
}

// handleSetDisappearing implements "message.setDisappearing" (FR-033, FR-016).
// Writes an AuditDisappearingSet entry on success. The 4-value enum is
// enforced in the use case — the adapter re-validates but callers get a
// -32602 invalid_params instead of -32000 upstream_error for bad input.
func (d *Dispatcher) handleSetDisappearing(ctx context.Context, raw json.RawMessage) (json.RawMessage, error) {
	return d.idempotentCall(ctx, "message.setDisappearing", raw, func(ctx context.Context) (json.RawMessage, error) {
		return d.doSetDisappearing(ctx, raw)
	})
}

func (d *Dispatcher) doSetDisappearing(ctx context.Context, raw json.RawMessage) (json.RawMessage, error) {
	ds, ok := d.disappearingSetter()
	if !ok {
		return nil, ErrMethodNotFound
	}
	var p setDisappearingParams
	if err := parseParams(raw, &p); err != nil {
		return nil, err
	}
	if p.Chat == "" {
		return nil, ErrInvalidParams
	}
	if _, ok := legalDisappearing[p.Seconds]; !ok {
		return nil, ErrInvalidParams
	}
	jid, err := domain.Parse(p.Chat)
	if err != nil {
		return nil, ErrInvalidJID
	}
	if err := d.checkSafetyAndAudit(ctx, jid, domain.ActionChatState); err != nil {
		return nil, err
	}
	if err := ds.SetDisappearing(ctx, jid, p.Seconds); err != nil {
		d.recordModerationAudit(ctx, domain.AuditDisappearingSet, jid, "error", err.Error())
		return nil, fmt.Errorf("message.setDisappearing: %w", err)
	}
	d.recordModerationAudit(ctx, domain.AuditDisappearingSet, jid, "ok", fmt.Sprintf("%ds", p.Seconds))
	return json.Marshal(struct{}{})
}

// disappearingSetter returns the DisappearingSetter surface on d.sender
// (if it satisfies the interface). Mirrors d.presence() — callers use the
// boolean to distinguish "port not wired" from "adapter returned an error".
func (d *Dispatcher) disappearingSetter() (DisappearingSetter, bool) {
	if d.sender == nil {
		return nil, false
	}
	ds, ok := d.sender.(DisappearingSetter)
	return ds, ok
}
