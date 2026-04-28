package memory

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/yolo-labz/wa/v2/internal/domain"
)

// MessageModerator is the in-memory implementation of app.MessageModerator
// (feature 018 FR-014/FR-015). It persists revoked/edited message state in
// a map so tests can observe the effect without wiring a real database.
type MessageModerator struct {
	mu      sync.Mutex
	revoked map[revokeKey]domain.RevokeScope
	edits   map[revokeKey]string
}

type revokeKey struct {
	chat domain.JID
	id   domain.MessageID
}

// NewMessageModerator returns an empty fake.
func NewMessageModerator() *MessageModerator {
	return &MessageModerator{
		revoked: make(map[revokeKey]domain.RevokeScope),
		edits:   make(map[revokeKey]string),
	}
}

// Revoke implements app.MessageModerator.
func (m *MessageModerator) Revoke(ctx context.Context, chat domain.JID, id domain.MessageID, scope domain.RevokeScope) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if chat.IsZero() {
		return fmt.Errorf("MessageModerator.Revoke: %w", domain.ErrInvalidJID)
	}
	if id == "" {
		return errors.New("MessageModerator.Revoke: empty message id")
	}
	if !scope.IsValid() {
		return fmt.Errorf("MessageModerator.Revoke: invalid scope %d", uint8(scope))
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.revoked[revokeKey{chat: chat, id: id}] = scope
	return nil
}

// Edit implements app.MessageModerator. The fake accepts any body up to
// 64 KB (the domain text limit); adapters with a 15-min window check
// layer that on top of this.
func (m *MessageModerator) Edit(ctx context.Context, chat domain.JID, id domain.MessageID, newBody string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if chat.IsZero() {
		return fmt.Errorf("MessageModerator.Edit: %w", domain.ErrInvalidJID)
	}
	if id == "" {
		return errors.New("MessageModerator.Edit: empty message id")
	}
	if len(newBody) > memoryMaxEditBytes {
		return fmt.Errorf("%w: edit body %d > %d bytes", domain.ErrMessageTooLarge, len(newBody), memoryMaxEditBytes)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.edits[revokeKey{chat: chat, id: id}] = newBody
	return nil
}

// memoryMaxEditBytes mirrors the domain text-message cap so Edit behaves
// like Send for body-size purposes.
const memoryMaxEditBytes = 64 * 1024

// RevokedScope returns the scope stamped by the last successful Revoke
// for (chat, id), or the zero value if none. Test-only accessor.
func (m *MessageModerator) RevokedScope(chat domain.JID, id domain.MessageID) domain.RevokeScope {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.revoked[revokeKey{chat: chat, id: id}]
}

// EditedBody returns the body stamped by the last successful Edit for
// (chat, id), or "" if none. Test-only accessor.
func (m *MessageModerator) EditedBody(chat domain.JID, id domain.MessageID) string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.edits[revokeKey{chat: chat, id: id}]
}
