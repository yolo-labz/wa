package whatsmeow

import (
	"context"

	"github.com/yolo-labz/wa/v2/internal/app"
	"github.com/yolo-labz/wa/v2/internal/domain"
)

// IdentityResolverAdapter satisfies `app.IdentityResolver` by delegating
// to whatsmeow's `Client.Store.LIDs` interface. Spec 106.
//
// FR-002 of spec 106: a successful resolution that returns the zero
// whatsmeow JID is translated to the zero domain JID — callers MUST
// treat absence as "no mapping yet known", not as an error.
type IdentityResolverAdapter struct {
	client whatsmeowClient
}

// NewIdentityResolver returns an IdentityResolverAdapter bound to the
// adapter's wrapped whatsmeow client.
func (a *Adapter) NewIdentityResolver() *IdentityResolverAdapter {
	return &IdentityResolverAdapter{client: a.client}
}

// ResolveLID returns the LID associated with pn, or the zero JID if no
// mapping is known. pn MUST satisfy `IsUser()` per the port contract.
func (r *IdentityResolverAdapter) ResolveLID(ctx context.Context, pn domain.JID) (domain.JID, error) {
	if !pn.IsUser() {
		return domain.JID{}, app.ErrNotIdentity
	}
	waPN := toWhatsmeow(pn)
	lid, err := r.client.GetLIDForPN(ctx, waPN)
	if err != nil {
		return domain.JID{}, err
	}
	if lid.IsEmpty() {
		return domain.JID{}, nil
	}
	return toDomain(lid)
}

// ResolvePN returns the PN associated with lid, or the zero JID if no
// mapping is known. lid MUST satisfy `IsLID()` per the port contract.
func (r *IdentityResolverAdapter) ResolvePN(ctx context.Context, lid domain.JID) (domain.JID, error) {
	if !lid.IsLID() {
		return domain.JID{}, app.ErrNotIdentity
	}
	waLID := toWhatsmeow(lid)
	pn, err := r.client.GetPNForLID(ctx, waLID)
	if err != nil {
		return domain.JID{}, err
	}
	if pn.IsEmpty() {
		return domain.JID{}, nil
	}
	return toDomain(pn)
}

// RecordMapping stores a (pn, lid) pair in whatsmeow's LIDStore.
func (r *IdentityResolverAdapter) RecordMapping(ctx context.Context, pn, lid domain.JID) error {
	if !pn.IsUser() || !lid.IsLID() {
		return app.ErrNotIdentity
	}
	return r.client.PutLIDMapping(ctx, toWhatsmeow(lid), toWhatsmeow(pn))
}

// Compile-time assertion.
var _ app.IdentityResolver = (*IdentityResolverAdapter)(nil)
