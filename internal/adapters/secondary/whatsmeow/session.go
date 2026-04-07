package whatsmeow

import (
	"context"

	"github.com/yolo-labz/wa/internal/domain"
)

// Load implements app.SessionStore. It returns the overlay session. The
// real pairing state lives inside whatsmeow's sqlstore.Container and is
// reflected into the overlay by the pair-flow in commit 5; this keeps
// SessionStore as a thin port consumers can mock.
//
// Per ports.go §SessionStore: returns a zero Session (NOT an error) when
// no session exists.
func (a *Adapter) Load(ctx context.Context) (domain.Session, error) {
	if err := ctx.Err(); err != nil {
		return domain.Session{}, err
	}
	a.overlayMu.Lock()
	defer a.overlayMu.Unlock()
	return a.seedSession, nil
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
