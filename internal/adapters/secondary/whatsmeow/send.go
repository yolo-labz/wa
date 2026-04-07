package whatsmeow

import (
	"context"
	"errors"
	"fmt"
	"os"

	waE2E "go.mau.fi/whatsmeow/proto/waE2E"
	"google.golang.org/protobuf/proto"

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
func (a *Adapter) Send(ctx context.Context, msg domain.Message) (domain.MessageID, error) {
	if msg == nil {
		return "", fmt.Errorf("MessageSender.Send: nil message")
	}
	if err := msg.Validate(); err != nil {
		return "", fmt.Errorf("MessageSender.Send: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if a.closed.Load() {
		return "", fmt.Errorf("MessageSender.Send: %w", domain.ErrDisconnected)
	}
	if !a.client.IsConnected() {
		return "", fmt.Errorf("MessageSender.Send: %w", domain.ErrDisconnected)
	}

	to := toWhatsmeow(msg.To())
	waMsg, err := buildOutboundMessage(msg)
	if err != nil {
		return "", fmt.Errorf("MessageSender.Send: %w", err)
	}

	// Use clientCtx-derived timeout? No — the port contract says honour
	// the caller's ctx. The whatsmeow client itself is governed by
	// clientCtx for its connection state, but per-call RPCs take the
	// caller's context.
	resp, err := a.client.SendMessage(ctx, to, waMsg)
	if err != nil {
		a.recordAuditDetail(domain.AuditSend, msg.To(), "error", err.Error())
		return "", fmt.Errorf("MessageSender.Send: %w", err)
	}

	a.recordAuditDetail(domain.AuditSend, msg.To(), "ok", string(resp.ID))
	return domain.MessageID(resp.ID), nil
}

// buildOutboundMessage maps a domain.Message onto a whatsmeow
// *waE2E.Message. Only the three domain variants are supported — the
// sealed sum type guarantees exhaustive coverage.
func buildOutboundMessage(msg domain.Message) (*waE2E.Message, error) {
	switch m := msg.(type) {
	case domain.TextMessage:
		return &waE2E.Message{
			Conversation: proto.String(m.Body),
		}, nil
	case domain.MediaMessage:
		// MS6: verify the path exists before any upload. The adapter is
		// the correct place for this check because internal/domain has
		// no os import.
		if _, err := os.Stat(m.Path); err != nil {
			if errors.Is(err, os.ErrNotExist) {
				return nil, fmt.Errorf("media path: %w", err)
			}
			return nil, fmt.Errorf("media stat: %w", err)
		}
		// Commit 4 only builds the protobuf stub; full upload plumbing
		// (Client.Upload + ImageMessage/VideoMessage discrimination by
		// mime) lands in a later commit. For now, surface a typed
		// "not yet implemented" so tests that exercise this path can
		// assert on it without hitting a real upload.
		return nil, errors.New("MediaMessage send not yet implemented in commit 4")
	case domain.ReactionMessage:
		// ReactionMessage plumbing needs the target chat's message key,
		// which requires more protocol wiring than commit 4 covers.
		return nil, errors.New("ReactionMessage send not yet implemented in commit 4")
	default:
		return nil, fmt.Errorf("unknown domain.Message variant: %T", msg)
	}
}
