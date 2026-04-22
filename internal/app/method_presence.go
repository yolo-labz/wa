package app

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/yolo-labz/wa/internal/domain"
)

// presenceChatParams is the JSON-RPC params common to all four presence
// methods: presence.composing.start/stop, presence.recording.start/stop
// (FR-032). Presence updates are fire-and-forget by spec — no idempotency
// key, no per-call rate limiter surfacing (the adapter drops excess
// silently per PresenceSender's contract).
type presenceChatParams struct {
	Chat string `json:"chat"`
}

// handlePresenceComposingStart implements "presence.composing.start" —
// routes to PresenceSender.SendComposing(state="composing").
func (d *Dispatcher) handlePresenceComposingStart(ctx context.Context, raw json.RawMessage) (json.RawMessage, error) {
	return d.doPresence(ctx, raw, "composing")
}

// handlePresenceComposingStop implements "presence.composing.stop".
// The whatsmeow wire state for "no longer typing" is "paused"; the adapter
// translates both composing-stop and recording-stop into the single upstream
// `paused` frame per FR-032. Emitting two distinct wire methods keeps the
// caller surface ergonomic without exposing WhatsApp's tri-state model.
func (d *Dispatcher) handlePresenceComposingStop(ctx context.Context, raw json.RawMessage) (json.RawMessage, error) {
	return d.doPresence(ctx, raw, "paused")
}

// handlePresenceRecordingStart implements "presence.recording.start".
func (d *Dispatcher) handlePresenceRecordingStart(ctx context.Context, raw json.RawMessage) (json.RawMessage, error) {
	return d.doPresence(ctx, raw, "recording")
}

// handlePresenceRecordingStop implements "presence.recording.stop".
func (d *Dispatcher) handlePresenceRecordingStop(ctx context.Context, raw json.RawMessage) (json.RawMessage, error) {
	return d.doPresence(ctx, raw, "paused")
}

// doPresence is the shared body for all four presence methods. ctx is the
// request context; raw is the JSON-RPC params; state is the wire state the
// adapter will forward.
func (d *Dispatcher) doPresence(ctx context.Context, raw json.RawMessage, state string) (json.RawMessage, error) {
	ps, ok := d.presence()
	if !ok {
		return nil, ErrMethodNotFound
	}
	var p presenceChatParams
	if err := parseParams(raw, &p); err != nil {
		return nil, err
	}
	if p.Chat == "" {
		return nil, ErrInvalidParams
	}
	jid, err := domain.Parse(p.Chat)
	if err != nil {
		return nil, ErrInvalidJID
	}
	if err := d.checkSafetyAndAudit(ctx, jid, domain.ActionPresence); err != nil {
		return nil, err
	}
	if err := ps.SendComposing(ctx, jid, state, 0); err != nil {
		return nil, fmt.Errorf("presence.%s: %w", state, err)
	}
	return json.Marshal(struct{}{})
}
