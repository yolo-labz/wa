package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/yolo-labz/wa/v2/internal/domain"
	"github.com/yolo-labz/wa/v2/internal/observability"
)

// sendParams is the JSON-RPC params for the "send" method.
type sendParams struct {
	To   string `json:"to"`
	Body string `json:"body"`
}

// sendMediaParams is the JSON-RPC params for the "sendMedia" method.
type sendMediaParams struct {
	To      string `json:"to"`
	Path    string `json:"path"`
	Caption string `json:"caption,omitempty"`
	Mime    string `json:"mime,omitempty"`
}

// reactParams is the JSON-RPC params for the "react" method.
type reactParams struct {
	Chat      string `json:"chat"`
	MessageID string `json:"messageId"`
	Emoji     string `json:"emoji"`
}

// sendResult is the JSON-RPC result for "send" and "sendMedia".
type sendResult struct {
	MessageID string `json:"messageId"`
	Timestamp int64  `json:"timestamp"`
}

// handleSend implements the "send" JSON-RPC method: parse params, run
// safety pipeline, call MessageSender.Send with a TextMessage, audit.
// Wrapped in the FR-034a idempotency sidecar — a non-empty
// `idempotencyKey` in params replays the cached bytes on retry; a mismatched
// hash under the same key yields -32101 idempotency_collision.
func (d *Dispatcher) handleSend(ctx context.Context, raw json.RawMessage) (json.RawMessage, error) {
	return d.idempotentCall(ctx, "send", raw, func(ctx context.Context) (json.RawMessage, error) {
		return d.doSend(ctx, raw)
	})
}

func (d *Dispatcher) doSend(ctx context.Context, raw json.RawMessage) (json.RawMessage, error) {
	var p sendParams
	if err := parseParams(raw, &p); err != nil {
		return nil, err
	}
	if p.To == "" || p.Body == "" {
		return nil, ErrInvalidParams
	}

	jid, err := domain.Parse(p.To)
	if err != nil {
		return nil, ErrInvalidJID
	}

	ctx, span := observability.StartSend(ctx, d.profile, "send", p.To)
	defer span.End()

	// Safety pipeline: allowlist + rate limiter.
	if err := d.checkSafetyAndAudit(ctx, jid, domain.ActionSend); err != nil {
		return nil, err
	}
	if err := d.ensureNotBlocked(ctx, jid); err != nil {
		d.recordAudit(ctx, jid, "denied:blocked", "")
		return nil, err
	}

	msg := domain.TextMessage{Recipient: jid, Body: p.Body}
	id, err := d.sender.Send(ctx, msg)
	if err != nil {
		d.recordAudit(ctx, jid, "error", err.Error())
		return nil, fmt.Errorf("send: %w", err)
	}

	d.recordAudit(ctx, jid, "ok", string(id))

	return marshalResult(sendResult{
		MessageID: string(id),
		Timestamp: time.Now().Unix(),
	})
}

// handleSendMedia implements the "sendMedia" JSON-RPC method.
// Wrapped in the FR-034a idempotency sidecar.
func (d *Dispatcher) handleSendMedia(ctx context.Context, raw json.RawMessage) (json.RawMessage, error) {
	return d.idempotentCall(ctx, "sendMedia", raw, func(ctx context.Context) (json.RawMessage, error) {
		return d.doSendMedia(ctx, raw)
	})
}

func (d *Dispatcher) doSendMedia(ctx context.Context, raw json.RawMessage) (json.RawMessage, error) {
	var p sendMediaParams
	if err := parseParams(raw, &p); err != nil {
		return nil, err
	}
	if p.To == "" || p.Path == "" {
		return nil, ErrInvalidParams
	}

	jid, err := domain.Parse(p.To)
	if err != nil {
		return nil, ErrInvalidJID
	}

	ctx, span := observability.StartSend(ctx, d.profile, "sendMedia", p.To)
	defer span.End()

	if err := d.checkSafetyAndAudit(ctx, jid, domain.ActionSend); err != nil {
		return nil, err
	}
	if err := d.ensureNotBlocked(ctx, jid); err != nil {
		d.recordAudit(ctx, jid, "denied:blocked", "")
		return nil, err
	}

	mime := p.Mime
	if mime == "" {
		mime = "application/octet-stream"
	}

	msg := domain.MediaMessage{Recipient: jid, Path: p.Path, Mime: mime, Caption: p.Caption}
	id, err := d.sender.Send(ctx, msg)
	if err != nil {
		d.recordAudit(ctx, jid, "error", err.Error())
		return nil, fmt.Errorf("sendMedia: %w", err)
	}

	d.recordAudit(ctx, jid, "ok", string(id))

	return marshalResult(sendResult{
		MessageID: string(id),
		Timestamp: time.Now().Unix(),
	})
}

// handleReact implements the "react" JSON-RPC method.
// Wrapped in the FR-034a idempotency sidecar.
func (d *Dispatcher) handleReact(ctx context.Context, raw json.RawMessage) (json.RawMessage, error) {
	return d.idempotentCall(ctx, "react", raw, func(ctx context.Context) (json.RawMessage, error) {
		return d.doReact(ctx, raw)
	})
}

func (d *Dispatcher) doReact(ctx context.Context, raw json.RawMessage) (json.RawMessage, error) {
	var p reactParams
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

	ctx, span := observability.StartSend(ctx, d.profile, "react", p.Chat)
	defer span.End()

	if err := d.checkSafetyAndAudit(ctx, jid, domain.ActionSend); err != nil {
		return nil, err
	}
	if err := d.ensureNotBlocked(ctx, jid); err != nil {
		d.recordAudit(ctx, jid, "denied:blocked", "")
		return nil, err
	}

	msg := domain.ReactionMessage{
		Recipient: jid,
		TargetID:  domain.MessageID(p.MessageID),
		Emoji:     p.Emoji,
	}
	_, err = d.sender.Send(ctx, msg)
	if err != nil {
		d.recordAudit(ctx, jid, "error", err.Error())
		return nil, fmt.Errorf("react: %w", err)
	}

	d.recordAudit(ctx, jid, "ok", "")

	return json.Marshal(struct{}{})
}

// checkSafetyAndAudit runs the safety pipeline and records an audit entry
// on denial. Returns nil if the action is allowed.
func (d *Dispatcher) checkSafetyAndAudit(ctx context.Context, jid domain.JID, action domain.Action) error {
	err := d.safety.Check(jid, action)
	if err == nil {
		return nil
	}

	// Determine the denial reason for the audit entry.
	var decision string
	metrics := observability.GetMetrics()
	switch {
	case errors.Is(err, ErrNotAllowlisted):
		decision = "denied:allowlist"
		metrics.RecordAllowlistRefusal(action.String())
	case errors.Is(err, ErrWarmupActive):
		decision = "denied:warmup"
		metrics.RecordRateLimitRefusal(action.String())
	case errors.Is(err, ErrRateLimited):
		decision = "denied:rate"
		metrics.RecordRateLimitRefusal(action.String())
	default:
		decision = "denied:unknown"
	}

	d.recordAudit(ctx, jid, decision, "")
	return err
}

// recordAudit records an audit event; errors are logged but do not fail
// the caller's request (FR-037).
func (d *Dispatcher) recordAudit(ctx context.Context, jid domain.JID, decision, detail string) {
	evt := domain.NewAuditEvent("dispatcher", domain.AuditSend, jid, decision, detail)
	if err := d.audit.Record(ctx, evt); err != nil {
		d.log.Error("audit write failed", "err", err)
	}
}

// parseParams unmarshals raw JSON params into dst. Returns ErrInvalidParams
// on nil/empty input or JSON parse errors.
func parseParams(raw json.RawMessage, dst any) error {
	if len(raw) == 0 {
		return ErrInvalidParams
	}
	if err := json.Unmarshal(raw, dst); err != nil {
		return ErrInvalidParams
	}
	return nil
}

// marshalResult is a convenience wrapper for json.Marshal.
func marshalResult(v any) (json.RawMessage, error) {
	return json.Marshal(v)
}
