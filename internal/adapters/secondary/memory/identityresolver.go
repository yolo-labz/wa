package memory

import (
	"context"
	"sync"

	"github.com/yolo-labz/wa/v2/internal/app"
	"github.com/yolo-labz/wa/v2/internal/domain"
)

// IdentityResolver is the in-memory implementation of
// `app.IdentityResolver`. Used by tests and as a deterministic stand-in
// when whatsmeow's LIDStore is unavailable. Spec 106.
//
// The resolver keeps two parallel maps so both directions are O(1).
// Repeat RecordMapping calls with the same pair are idempotent;
// conflicting mappings (same PN with different LID, or same LID with
// different PN) overwrite — the test harness explicitly seeds whatever
// arrangement it wants to model.
type IdentityResolver struct {
	mu      sync.RWMutex
	pnToLID map[domain.JID]domain.JID
	lidToPN map[domain.JID]domain.JID
}

// NewIdentityResolver returns an empty IdentityResolver.
func NewIdentityResolver() *IdentityResolver {
	return &IdentityResolver{
		pnToLID: make(map[domain.JID]domain.JID),
		lidToPN: make(map[domain.JID]domain.JID),
	}
}

// ResolveLID returns the LID for pn, or the zero JID if unknown.
func (r *IdentityResolver) ResolveLID(_ context.Context, pn domain.JID) (domain.JID, error) {
	if !pn.IsUser() {
		return domain.JID{}, app.ErrNotIdentity
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.pnToLID[pn], nil
}

// ResolvePN returns the PN for lid, or the zero JID if unknown.
func (r *IdentityResolver) ResolvePN(_ context.Context, lid domain.JID) (domain.JID, error) {
	if !lid.IsLID() {
		return domain.JID{}, app.ErrNotIdentity
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.lidToPN[lid], nil
}

// RecordMapping stores the (pn, lid) pair in both directions.
func (r *IdentityResolver) RecordMapping(_ context.Context, pn, lid domain.JID) error {
	if !pn.IsUser() || !lid.IsLID() {
		return app.ErrNotIdentity
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.pnToLID[pn] = lid
	r.lidToPN[lid] = pn
	return nil
}

// Compile-time assertion.
var _ app.IdentityResolver = (*IdentityResolver)(nil)
