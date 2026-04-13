package whatsmeow

import (
	"context"
	"time"

	"github.com/yolo-labz/wa/internal/domain"
)

// Load implements app.SessionStore. It returns the live session derived
// from the whatsmeow client's Store.ID (the real pairing state lives
// inside whatsmeow's sqlstore.Container). If the client is not paired,
// falls back to the overlay (which tests can seed).
//
// Per ports.go §SessionStore: returns a zero Session (NOT an error) when
// no session exists.
func (a *Adapter) Load(ctx context.Context) (domain.Session, error) {
	if err := ctx.Err(); err != nil {
		return domain.Session{}, err
	}
	// Prefer the live whatsmeow device when available.
	if sess, ok := a.liveSession(); ok {
		return sess, nil
	}
	// Fall back to the overlay (used by tests and for not-yet-paired state).
	a.overlayMu.Lock()
	defer a.overlayMu.Unlock()
	return a.seedSession, nil
}

// liveSession extracts a domain.Session from the whatsmeow client's
// device store, returning false if no paired device is available.
func (a *Adapter) liveSession() (domain.Session, bool) {
	if a.client == nil {
		return domain.Session{}, false
	}
	device := a.client.Store()
	if device == nil || device.ID == nil {
		return domain.Session{}, false
	}
	bare := device.ID.ToNonAD()
	jid, err := toDomain(bare)
	if err != nil {
		return domain.Session{}, false
	}
	// NewSession requires deviceID > 0; default to 1 if whatsmeow
	// reports 0 (primary device before full registration).
	devID := device.ID.Device
	if devID == 0 {
		devID = 1
	}
	sess, err := domain.NewSession(jid, devID, time.Now().UTC())
	if err != nil {
		return domain.Session{}, false
	}
	return sess, true
}

// Save implements app.SessionStore. Writes to the overlay.
func (a *Adapter) Save(ctx context.Context, s domain.Session) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	a.overlayMu.Lock()
	defer a.overlayMu.Unlock()
	a.seedSession = s
	return nil
}

// Clear implements app.SessionStore. Idempotent.
func (a *Adapter) Clear(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return a.clearSessionLocked()
}
