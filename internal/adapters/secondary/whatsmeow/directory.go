package whatsmeow

import (
	"context"
	"fmt"

	"github.com/yolo-labz/wa/internal/domain"
)

// Lookup implements app.ContactDirectory. It consults the overlay first
// (for deterministic tests under //go:build integration) and falls back
// to the whatsmeow store's ContactStore. The overlay path is NOT a
// caching layer — it exists exclusively for test seeding.
func (a *Adapter) Lookup(ctx context.Context, jid domain.JID) (domain.Contact, error) {
	if err := ctx.Err(); err != nil {
		return domain.Contact{}, err
	}
	if jid.IsZero() {
		return domain.Contact{}, fmt.Errorf("ContactDirectory.Lookup: %w", domain.ErrInvalidJID)
	}

	a.overlayMu.Lock()
	if c, ok := a.seedContacts[jid]; ok {
		a.overlayMu.Unlock()
		return c, nil
	}
	a.overlayMu.Unlock()

	// Fall back to the whatsmeow store. The store may be nil in unit
	// tests that use the fake client without seeding a *store.Device;
	// in that case we report ErrNotFound.
	device := a.client.Store()
	if device == nil || device.Contacts == nil {
		return domain.Contact{}, fmt.Errorf("%w: %s", ErrNotFound, jid)
	}
	info, err := device.Contacts.GetContact(ctx, toWhatsmeow(jid))
	if err != nil {
		return domain.Contact{}, fmt.Errorf("ContactDirectory.Lookup: %w", err)
	}
	if !info.Found {
		return domain.Contact{}, fmt.Errorf("%w: %s", ErrNotFound, jid)
	}
	name := info.PushName
	if name == "" {
		name = info.FullName
	}
	return domain.Contact{
		JID:      jid,
		PushName: name,
	}, nil
}

// Resolve implements app.ContactDirectory. It delegates to
// domain.ParsePhone without any network call — phone-number resolution
// via whatsmeow's contact registration mode requires a round-trip the
// adapter does not currently perform.
func (a *Adapter) Resolve(ctx context.Context, phone string) (domain.JID, error) {
	if err := ctx.Err(); err != nil {
		return domain.JID{}, err
	}
	return domain.ParsePhone(phone)
}
