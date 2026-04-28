package memory

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/yolo-labz/wa/v2/internal/domain"
)

// ChatStateManager is the in-memory implementation of
// app.ChatStateManager (feature 018 FR-016).
type ChatStateManager struct {
	clock Clock

	mu         sync.Mutex
	archived   map[domain.JID]bool
	pinned     map[domain.JID]bool
	mutedUntil map[domain.JID]*time.Time
	unread     map[domain.JID]bool
}

// NewChatStateManager returns an empty fake using clk for time checks.
// A nil clk falls back to RealClock.
func NewChatStateManager(clk Clock) *ChatStateManager {
	if clk == nil {
		clk = RealClock{}
	}
	return &ChatStateManager{
		clock:      clk,
		archived:   make(map[domain.JID]bool),
		pinned:     make(map[domain.JID]bool),
		mutedUntil: make(map[domain.JID]*time.Time),
		unread:     make(map[domain.JID]bool),
	}
}

// Archive implements app.ChatStateManager. Idempotent on same value.
func (m *ChatStateManager) Archive(ctx context.Context, chat domain.JID, archived bool) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if chat.IsZero() {
		return fmt.Errorf("ChatStateManager.Archive: %w", domain.ErrInvalidJID)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.archived[chat] = archived
	return nil
}

// Mute implements app.ChatStateManager. nil unmutes; past timestamp is
// rejected.
func (m *ChatStateManager) Mute(ctx context.Context, chat domain.JID, until *time.Time) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if chat.IsZero() {
		return fmt.Errorf("ChatStateManager.Mute: %w", domain.ErrInvalidJID)
	}
	if until != nil && !until.After(m.clock.Now()) {
		return fmt.Errorf("ChatStateManager.Mute: until=%s is not in the future", until.UTC().Format(time.RFC3339))
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.mutedUntil[chat] = until
	return nil
}

// Pin implements app.ChatStateManager.
func (m *ChatStateManager) Pin(ctx context.Context, chat domain.JID, pinned bool) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if chat.IsZero() {
		return fmt.Errorf("ChatStateManager.Pin: %w", domain.ErrInvalidJID)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.pinned[chat] = pinned
	return nil
}

// MarkUnread implements app.ChatStateManager. Idempotent.
func (m *ChatStateManager) MarkUnread(ctx context.Context, chat domain.JID) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if chat.IsZero() {
		return fmt.Errorf("ChatStateManager.MarkUnread: %w", domain.ErrInvalidJID)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.unread[chat] = true
	return nil
}

// IsArchived reports the last archived state for chat. Test accessor.
func (m *ChatStateManager) IsArchived(chat domain.JID) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.archived[chat]
}

// IsPinned reports the last pin state for chat. Test accessor.
func (m *ChatStateManager) IsPinned(chat domain.JID) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.pinned[chat]
}

// MutedUntil reports the last mute timestamp for chat (nil = unmuted).
// Test accessor.
func (m *ChatStateManager) MutedUntil(chat domain.JID) *time.Time {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.mutedUntil[chat]
}
