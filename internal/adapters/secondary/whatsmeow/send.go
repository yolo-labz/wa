package whatsmeow

import (
	"context"
	"fmt"

	waE2E "go.mau.fi/whatsmeow/proto/waE2E"

	"github.com/yolo-labz/wa/internal/domain"
)

// Send implements app.MessageSender. Contract (ports.go §MessageSender):
//   - MS2/MS3: Validate the domain message BEFORE any I/O.
//   - MS4: honour ctx cancellation.
//   - MS1: return a non-zero domain.MessageID on success.
//   - MS5: safe for concurrent use (the underlying whatsmeow client is).
//   - MS6: MediaMessage with a missing path returns a wrapped os.ErrNotExist.
//
// FR-018: eager domain.ErrDisconnected when the adapter is closed or the
// underlying client reports !IsConnected. The caller (a use case in
// internal/app) decides whether to retry or surface the failure — the
// adapter never queues silently.
func sendWrap(err error) error { return fmt.Errorf("MessageSender.Send: %w", err) }

// Send dispatches an outbound message through the whatsmeow client.
func (a *Adapter) Send(ctx context.Context, msg domain.Message) (domain.MessageID, error) {
	if msg == nil {
		return "", sendWrap(fmt.Errorf("nil message"))
	}
	if err := msg.Validate(); err != nil {
		return "", sendWrap(err)
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if a.closed.Load() {
		return "", sendWrap(domain.ErrDisconnected)
	}
	if !a.client.IsConnected() {
		return "", sendWrap(domain.ErrDisconnected)
	}

	to := toWhatsmeow(msg.To())
	waMsg, err := a.buildOutboundMessage(ctx, msg)
	if err != nil {
		return "", sendWrap(err)
	}

	// Use clientCtx-derived timeout? No — the port contract says honour
	// the caller's ctx. The whatsmeow client itself is governed by
	// clientCtx for its connection state, but per-call RPCs take the
	// caller's context.
	resp, err := a.client.SendMessage(ctx, to, waMsg)
	if err != nil {
		a.recordAuditDetail(domain.AuditSend, msg.To(), "error", err.Error())
		return "", sendWrap(err)
	}

	a.recordAuditDetail(domain.AuditSend, msg.To(), "ok", string(resp.ID))

	// Feature 009 — FR-004: persist outbound messages to messages.db.
	if a.history != nil {
		body := ""
		mediaType := ""
		caption := ""
		switch m := msg.(type) {
		case domain.TextMessage:
			body = m.Body
		case domain.MediaMessage:
			mediaType = m.Mime
			caption = m.Caption
			body = caption
		}
		ownJID := ""
		if dev := a.client.Store(); dev != nil && dev.ID != nil {
			ownJID = dev.ID.String()
		}
		if err := a.history.InsertRaw(ctx,
			msg.To().String(), ownJID, string(resp.ID), resp.Timestamp.Unix(),
			body, mediaType, caption, "", true, nil,
		); err != nil {
			a.recordAuditDetail(domain.AuditPanic, msg.To(), "persist_send", err.Error())
		}
	}

	return domain.MessageID(resp.ID), nil
}

// buildOutboundMessage maps a domain.Message onto a whatsmeow
// *waE2E.Message. Only the three domain variants are supported — the
// sealed sum type guarantees exhaustive coverage.
//
// MediaMessage routes through buildMediaMessage (feature 018 T1-08, R-01).
// ReactionMessage routes through buildReactionMessage (T1-09, R-02).
func (a *Adapter) buildOutboundMessage(ctx context.Context, msg domain.Message) (*waE2E.Message, error) {
	switch m := msg.(type) {
	case domain.TextMessage:
		return &waE2E.Message{
			Conversation: new(m.Body),
		}, nil
	case domain.MediaMessage:
		return buildMediaMessage(ctx, a.client, m)
	case domain.ReactionMessage:
		return buildReactionMessage(m, a.nowFn), nil
	default:
		return nil, fmt.Errorf("unknown domain.Message variant: %T", msg)
	}
}
