package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/yolo-labz/wa/v2/internal/domain"
)

// archiveParams is the JSON-RPC params for "chat.archive" (FR-016).
type archiveParams struct {
	Chat     string `json:"chat"`
	Archived bool   `json:"archived"`
}

// muteParams is the JSON-RPC params for "chat.mute" (FR-016). UntilUnix is
// the Unix-seconds deadline; 0 (or omitted) means unmute.
type muteParams struct {
	Chat      string `json:"chat"`
	UntilUnix int64  `json:"untilUnix,omitempty"`
}

// pinParams is the JSON-RPC params for "chat.pin" (FR-016).
type pinParams struct {
	Chat   string `json:"chat"`
	Pinned bool   `json:"pinned"`
}

// markUnreadParams is the JSON-RPC params for "chat.markUnread" (FR-017).
type markUnreadParams struct {
	Chat string `json:"chat"`
}

// handleChatArchive implements "chat.archive" (FR-016).
func (d *Dispatcher) handleChatArchive(ctx context.Context, raw json.RawMessage) (json.RawMessage, error) {
	return d.idempotentCall(ctx, "chat.archive", raw, func(ctx context.Context) (json.RawMessage, error) {
		return d.doChatArchive(ctx, raw)
	})
}

func (d *Dispatcher) doChatArchive(ctx context.Context, raw json.RawMessage) (json.RawMessage, error) {
	if d.chatState == nil {
		return nil, ErrMethodNotFound
	}
	var p archiveParams
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
	if err := d.checkSafetyAndAudit(ctx, jid, domain.ActionChatState); err != nil {
		return nil, err
	}
	if err := d.chatState.Archive(ctx, jid, p.Archived); err != nil {
		return nil, mapChatStateErr("chat.archive", err)
	}
	return json.Marshal(struct{}{})
}

// handleChatMute implements "chat.mute" (FR-016).
func (d *Dispatcher) handleChatMute(ctx context.Context, raw json.RawMessage) (json.RawMessage, error) {
	return d.idempotentCall(ctx, "chat.mute", raw, func(ctx context.Context) (json.RawMessage, error) {
		return d.doChatMute(ctx, raw)
	})
}

func (d *Dispatcher) doChatMute(ctx context.Context, raw json.RawMessage) (json.RawMessage, error) {
	if d.chatState == nil {
		return nil, ErrMethodNotFound
	}
	var p muteParams
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
	if err := d.checkSafetyAndAudit(ctx, jid, domain.ActionChatState); err != nil {
		return nil, err
	}
	var until *time.Time
	if p.UntilUnix != 0 {
		t := time.Unix(p.UntilUnix, 0).UTC()
		until = &t
	}
	if err := d.chatState.Mute(ctx, jid, until); err != nil {
		return nil, mapChatStateErr("chat.mute", err)
	}
	return json.Marshal(struct{}{})
}

// handleChatPin implements "chat.pin" (FR-016).
func (d *Dispatcher) handleChatPin(ctx context.Context, raw json.RawMessage) (json.RawMessage, error) {
	return d.idempotentCall(ctx, "chat.pin", raw, func(ctx context.Context) (json.RawMessage, error) {
		return d.doChatPin(ctx, raw)
	})
}

func (d *Dispatcher) doChatPin(ctx context.Context, raw json.RawMessage) (json.RawMessage, error) {
	if d.chatState == nil {
		return nil, ErrMethodNotFound
	}
	var p pinParams
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
	if err := d.checkSafetyAndAudit(ctx, jid, domain.ActionChatState); err != nil {
		return nil, err
	}
	if err := d.chatState.Pin(ctx, jid, p.Pinned); err != nil {
		return nil, mapChatStateErr("chat.pin", err)
	}
	return json.Marshal(struct{}{})
}

// handleChatMarkUnread implements "chat.markUnread" (FR-017).
func (d *Dispatcher) handleChatMarkUnread(ctx context.Context, raw json.RawMessage) (json.RawMessage, error) {
	return d.idempotentCall(ctx, "chat.markUnread", raw, func(ctx context.Context) (json.RawMessage, error) {
		return d.doChatMarkUnread(ctx, raw)
	})
}

func (d *Dispatcher) doChatMarkUnread(ctx context.Context, raw json.RawMessage) (json.RawMessage, error) {
	if d.chatState == nil {
		return nil, ErrMethodNotFound
	}
	var p markUnreadParams
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
	if err := d.checkSafetyAndAudit(ctx, jid, domain.ActionChatState); err != nil {
		return nil, err
	}
	if err := d.chatState.MarkUnread(ctx, jid); err != nil {
		return nil, mapChatStateErr("chat.markUnread", err)
	}
	return json.Marshal(struct{}{})
}

// mapChatStateErr preserves typed domain errors (ErrPastMuteTimestamp,
// ErrUpstreamError, ErrDisconnected) so the socket dispatcher can route
// them to their JSON-RPC codes. Wrapping with fmt.Errorf keeps errors.Is
// working across the boundary.
func mapChatStateErr(method string, err error) error {
	if errors.Is(err, domain.ErrPastMuteTimestamp) ||
		errors.Is(err, domain.ErrUpstreamError) ||
		errors.Is(err, domain.ErrInvalidJID) ||
		errors.Is(err, domain.ErrDisconnected) {
		return err
	}
	return fmt.Errorf("%s: %w", method, err)
}
