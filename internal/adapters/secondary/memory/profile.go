package memory

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/yolo-labz/wa/internal/domain"
)

// Profile identity-field limits mirror WhatsApp's server-side caps.
const (
	maxProfileNameBytes   = 25
	maxProfileStatusBytes = 139
)

// ProfileEditor is the in-memory implementation of app.ProfileEditor
// (feature 018 FR-027/FR-028).
type ProfileEditor struct {
	mu      sync.Mutex
	name    string
	status  string
	avatars map[avatarKey]string
}

type avatarKey struct {
	jid  domain.JID
	size domain.AvatarSize
}

// NewProfileEditor returns an empty fake with no pre-seeded avatars. The
// synthetic path convention is documented on GetAvatar.
func NewProfileEditor() *ProfileEditor {
	return &ProfileEditor{avatars: make(map[avatarKey]string)}
}

// SetName implements app.ProfileEditor.
func (p *ProfileEditor) SetName(ctx context.Context, name string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if len(name) > maxProfileNameBytes {
		return fmt.Errorf("%w: profile name %d > %d bytes", domain.ErrMessageTooLarge, len(name), maxProfileNameBytes)
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.name = name
	return nil
}

// SetStatus implements app.ProfileEditor.
func (p *ProfileEditor) SetStatus(ctx context.Context, status string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if len(status) > maxProfileStatusBytes {
		return fmt.Errorf("%w: profile status %d > %d bytes", domain.ErrMessageTooLarge, len(status), maxProfileStatusBytes)
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.status = status
	return nil
}

// GetAvatar implements app.ProfileEditor. Returns a synthetic path of the
// form "/mem/avatars/<jid>/<size>.jpg". SeedAvatar can override the path
// for a given (jid, size).
func (p *ProfileEditor) GetAvatar(ctx context.Context, jid domain.JID, size domain.AvatarSize) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if jid.IsZero() {
		return "", fmt.Errorf("ProfileEditor.GetAvatar: %w", domain.ErrInvalidJID)
	}
	if !size.IsValid() {
		return "", fmt.Errorf("ProfileEditor.GetAvatar: invalid size %d", uint8(size))
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if override, ok := p.avatars[avatarKey{jid: jid, size: size}]; ok {
		return override, nil
	}
	return fmt.Sprintf("/mem/avatars/%s/%s.jpg", jid.String(), size.String()), nil
}

// Name returns the last name set. Test accessor.
func (p *ProfileEditor) Name() string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.name
}

// Status returns the last status set. Test accessor.
func (p *ProfileEditor) Status() string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.status
}

// SeedAvatar overrides the synthetic path for (jid, size). Returns an
// error if jid is zero.
func (p *ProfileEditor) SeedAvatar(jid domain.JID, size domain.AvatarSize, path string) error {
	if jid.IsZero() {
		return errors.New("memory: seed avatar with zero JID")
	}
	if !size.IsValid() {
		return fmt.Errorf("memory: seed avatar with invalid size %d", uint8(size))
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.avatars[avatarKey{jid: jid, size: size}] = path
	return nil
}
