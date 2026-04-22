package whatsmeow

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"go.mau.fi/whatsmeow/appstate"

	"github.com/yolo-labz/wa/internal/domain"
)

// maxPinnedChats is whatsmeow's upstream cap on pinned chats. Surfaced as
// ErrUpstreamError before the appstate patch is sent — whatsmeow does not
// reject BuildPin synchronously, so we enforce locally to keep the wire
// contract deterministic.
const maxPinnedChats = 3

// ChatStateAdapter is the whatsmeow-backed implementation of
// app.ChatStateManager (FR-016, FR-017). Archive / Mute / Pin / MarkUnread
// are all encoded as appstate patches and pushed through SendAppState.
//
// The pinned-chat count is tracked locally so the 3-pin cap surfaces as a
// synchronous -32000 upstream_error instead of being silently dropped in
// the appstate acknowledge flow.
type ChatStateAdapter struct {
	client whatsmeowClient
	logger *slog.Logger
	nowFn  func() time.Time

	mu     sync.Mutex
	pinned map[domain.JID]struct{}
}

// NewChatStateFor wires a ChatStateAdapter to the Adapter's whatsmeow
// client + clock. Returns an error only if the client is nil (the
// composition root surfaces that as a startup failure rather than a
// nil-pointer panic later).
func (a *Adapter) NewChatStateFor() (*ChatStateAdapter, error) {
	if a == nil || a.client == nil {
		return nil, errors.New("whatsmeow.NewChatStateFor: nil adapter/client")
	}
	return &ChatStateAdapter{
		client: a.client,
		logger: a.logger,
		nowFn:  a.nowFn,
		pinned: make(map[domain.JID]struct{}),
	}, nil
}

// Archive implements app.ChatStateManager. Idempotent on repeated calls
// with the same value — whatsmeow's BuildArchive patch is server-side
// idempotent.
func (c *ChatStateAdapter) Archive(ctx context.Context, chat domain.JID, archived bool) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if chat.IsZero() {
		return fmt.Errorf("ChatStateAdapter.Archive: %w", domain.ErrInvalidJID)
	}
	if !c.client.IsConnected() {
		return fmt.Errorf("ChatStateAdapter.Archive: %w", domain.ErrDisconnected)
	}
	patch := appstate.BuildArchive(toWhatsmeow(chat), archived, c.nowFn(), nil)
	if err := c.client.SendAppState(ctx, patch); err != nil {
		return fmt.Errorf("ChatStateAdapter.Archive: %w", err)
	}
	return nil
}

// Mute implements app.ChatStateManager. A nil until unmutes; a past
// timestamp returns domain.ErrPastMuteTimestamp (→ -32100).
func (c *ChatStateAdapter) Mute(ctx context.Context, chat domain.JID, until *time.Time) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if chat.IsZero() {
		return fmt.Errorf("ChatStateAdapter.Mute: %w", domain.ErrInvalidJID)
	}
	if until != nil && !until.After(c.nowFn()) {
		return fmt.Errorf("%w: until=%s", domain.ErrPastMuteTimestamp, until.UTC().Format(time.RFC3339))
	}
	if !c.client.IsConnected() {
		return fmt.Errorf("ChatStateAdapter.Mute: %w", domain.ErrDisconnected)
	}

	var patch appstate.PatchInfo
	if until == nil {
		patch = appstate.BuildMuteAbs(toWhatsmeow(chat), false, nil)
	} else {
		unix := until.Unix()
		patch = appstate.BuildMuteAbs(toWhatsmeow(chat), true, &unix)
	}
	if err := c.client.SendAppState(ctx, patch); err != nil {
		return fmt.Errorf("ChatStateAdapter.Mute: %w", err)
	}
	return nil
}

// Pin implements app.ChatStateManager. The 3-pin cap is enforced locally
// via domain.ErrUpstreamError so the refusal is synchronous rather than
// buried in an async appstate ack.
func (c *ChatStateAdapter) Pin(ctx context.Context, chat domain.JID, pinned bool) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if chat.IsZero() {
		return fmt.Errorf("ChatStateAdapter.Pin: %w", domain.ErrInvalidJID)
	}

	c.mu.Lock()
	_, already := c.pinned[chat]
	if pinned && !already && len(c.pinned) >= maxPinnedChats {
		c.mu.Unlock()
		return fmt.Errorf("%w: pin cap %d exceeded", domain.ErrUpstreamError, maxPinnedChats)
	}
	c.mu.Unlock()

	if !c.client.IsConnected() {
		return fmt.Errorf("ChatStateAdapter.Pin: %w", domain.ErrDisconnected)
	}

	patch := appstate.BuildPin(toWhatsmeow(chat), pinned)
	if err := c.client.SendAppState(ctx, patch); err != nil {
		return fmt.Errorf("ChatStateAdapter.Pin: %w", err)
	}

	c.mu.Lock()
	defer c.mu.Unlock()
	if pinned {
		c.pinned[chat] = struct{}{}
	} else {
		delete(c.pinned, chat)
	}
	return nil
}

// MarkUnread implements app.ChatStateManager. Repeated calls are
// idempotent server-side.
func (c *ChatStateAdapter) MarkUnread(ctx context.Context, chat domain.JID) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if chat.IsZero() {
		return fmt.Errorf("ChatStateAdapter.MarkUnread: %w", domain.ErrInvalidJID)
	}
	if !c.client.IsConnected() {
		return fmt.Errorf("ChatStateAdapter.MarkUnread: %w", domain.ErrDisconnected)
	}
	patch := appstate.BuildMarkChatAsRead(toWhatsmeow(chat), false, c.nowFn(), nil)
	if err := c.client.SendAppState(ctx, patch); err != nil {
		return fmt.Errorf("ChatStateAdapter.MarkUnread: %w", err)
	}
	return nil
}
