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
//
// Humanize (roadmap 2.3) opts into the pre-send hygiene flow: composing
// presence → jittered human-scale delay → paused presence → send. Off by
// default; composes with the rate limiter (the token is consumed at
// request time), never replaces it.
type sendParams struct {
	To       string `json:"to"`
	Body     string `json:"body"`
	Humanize bool   `json:"humanize,omitempty"`
}

// sendMediaParams is the JSON-RPC params for the "sendMedia" method.
//
// Exactly one of Path, Bytes, SHA256 selects the payload source (spec 197
// "media byte seam"). Path keeps the original daemon-filesystem behaviour;
// Bytes lets a remote client inline the payload (Go's encoding/json decodes
// a JSON string into []byte via base64 automatically); SHA256 references an
// object already in the daemon's media store. The domain value enforces the
// XOR + size cap via SourceValidate.
type sendMediaParams struct {
	To      string `json:"to"`
	Path    string `json:"path,omitempty"`
	Caption string `json:"caption,omitempty"`
	Mime    string `json:"mime,omitempty"`
	// Filename is the display filename for document sends. Bytes/SHA256
	// sources have no daemon-visible path, so without it the recipient sees
	// an unopenable ".bin" attachment; its extension also feeds MIME
	// resolution when `mime` is absent.
	Filename string `json:"filename,omitempty"`
	// Bytes is the inline payload, base64-encoded on the wire (Go's
	// encoding/json maps a JSON string ↔ []byte through base64).
	Bytes []byte `json:"bytes,omitempty"`
	// SHA256 is the lowercase-hex content handle into the media store.
	SHA256 string `json:"sha256,omitempty"`
	// Humanize opts into the roadmap-2.3 pre-send hygiene flow (see
	// sendParams.Humanize). Delay scales with caption length.
	Humanize bool `json:"humanize,omitempty"`
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
	// Humanize runs after EVERY policy gate: a refused send must not leak
	// a typing indicator to the target.
	if p.Humanize {
		if err := d.humanizeBeforeSend(ctx, jid, len(p.Body)); err != nil {
			return nil, err
		}
	}

	msg := domain.TextMessage{Recipient: jid, Body: p.Body}
	id, err := d.sender.Send(ctx, msg)
	if err != nil {
		d.recordAudit(ctx, jid, "error", auditErrDetail(err))
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
	if p.To == "" {
		return nil, ErrInvalidParams
	}

	mime := p.Mime
	if mime == "" {
		// Domain.Validate requires a non-empty mime, so substitute the
		// generic sentinel. The whatsmeow adapter treats it as "detect":
		// filename/path extension, store-sniffed type, then magic bytes
		// (resolveOutboundMime). An explicit non-generic mime always wins.
		mime = "application/octet-stream"
	}

	msg := domain.MediaMessage{
		Path:     p.Path,
		Mime:     mime,
		Caption:  p.Caption,
		Filename: p.Filename,
		Bytes:    p.Bytes,
		SHA256:   p.SHA256,
	}
	// Surface the payload-source XOR + size cap as ErrInvalidParams (-32602)
	// at the param boundary, BEFORE the safety/rate-limit pipeline: >1 source
	// or oversize inline bytes is a malformed request that must not consume a
	// rate-limit token, and is distinct from the adapter-side -32016 too-large
	// on the wire. Recipient is set after this check (SourceValidate ignores it).
	if err := msg.SourceValidate(); err != nil {
		return nil, ErrInvalidParams
	}

	jid, err := domain.Parse(p.To)
	if err != nil {
		return nil, ErrInvalidJID
	}
	msg.Recipient = jid

	ctx, span := observability.StartSend(ctx, d.profile, "sendMedia", p.To)
	defer span.End()

	if err := d.checkSafetyAndAudit(ctx, jid, domain.ActionSend); err != nil {
		return nil, err
	}
	if err := d.ensureNotBlocked(ctx, jid); err != nil {
		d.recordAudit(ctx, jid, "denied:blocked", "")
		return nil, err
	}
	// Humanize runs after EVERY policy gate (see doSend). Delay scales
	// with the caption — the only typed content on a media send.
	if p.Humanize {
		if err := d.humanizeBeforeSend(ctx, jid, len(p.Caption)); err != nil {
			return nil, err
		}
	}

	id, err := d.sender.Send(ctx, msg)
	if err != nil {
		d.recordAudit(ctx, jid, "error", auditErrDetail(err))
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
		d.recordAudit(ctx, jid, "error", auditErrDetail(err))
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
		// Pre-send deliverability gate. The allowlist gates POLICY ("am I
		// allowed to message this JID"); it does NOT gate DELIVERABILITY
		// ("is this a real WhatsApp account"). A send to a fabricated /
		// scraped phone number was silently accepted (whatsmeow mints a
		// local message id) and never delivered. Only gates outbound sends.
		if action == domain.ActionSend {
			if waErr := d.ensureOnWhatsApp(ctx, jid); waErr != nil {
				d.recordAudit(ctx, jid, "denied:not-on-whatsapp", "")
				return waErr
			}
		}
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

// ensureOnWhatsApp is the pre-send deliverability gate (ErrNotOnWhatsApp).
// It blocks ONLY when the recipient is a phone-number JID
// (`@s.whatsapp.net`) that the server confirms has no WhatsApp account.
// @lid and group JIDs are skipped — they came from a real thread and are
// already reachable. The gate is fail-open by design: a nil checker (not
// wired) or any check error returns nil, so a transient IsOnWhatsApp
// failure never blocks a legitimate send — it only converts a *confirmed*
// non-account into a hard error instead of a silent fake-success.
func (d *Dispatcher) ensureOnWhatsApp(ctx context.Context, jid domain.JID) error {
	if d.onWhatsApp == nil || !jid.IsUser() {
		return nil
	}
	on, err := d.onWhatsApp.IsOnWhatsApp(ctx, jid.User())
	if err != nil {
		return nil
	}
	if !on {
		return ErrNotOnWhatsApp
	}
	return nil
}

// auditErrDetail collapses an upstream error to its typed class for
// the audit log (SEC-04): whatsmeow error strings frequently embed
// message bodies and recipient JIDs, and audit.log is append-only and
// never rotates — raw strings would persist leaked content forever.
// The full error still reaches the daemon log (bounded, rotatable) via
// the dispatcher error path; the audit row records only the code.
func auditErrDetail(err error) string {
	var coder codedError
	if errors.As(err, &coder) {
		return fmt.Sprintf("code=%d", coder.RPCCode())
	}
	return "internal"
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
