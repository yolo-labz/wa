package whatsmeow

import (
	"context"

	"github.com/yolo-labz/wa/internal/domain"
)

// Allows implements app.Allowlist by delegating to the embedded
// *domain.Allowlist. This is a pass-through; every allowlist mutation
// happens via *domain.Allowlist directly, not through the adapter.
//
// Per ports.go §Allowlist the decision is pure, synchronous, and takes
// no context — this is the only port that does not accept ctx.
func (a *Adapter) Allows(jid domain.JID, action domain.Action) bool {
	if a.allowlist == nil {
		return false
	}
	return a.allowlist.Allows(jid, action)
}

// Grant exposes allowlist mutation for the //go:build integration
// contract suite (it type-asserts for this pair on the adapter).
func (a *Adapter) Grant(jid domain.JID, actions ...domain.Action) {
	if a.allowlist == nil {
		return
	}
	a.allowlist.Grant(jid, actions...)
}

// Revoke exposes allowlist mutation for the contract test suite.
func (a *Adapter) Revoke(jid domain.JID, actions ...domain.Action) {
	if a.allowlist == nil {
		return
	}
	a.allowlist.Revoke(jid, actions...)
}

// Record implements app.AuditLog by delegating to the auditRingBuffer
// created in Open. The ring buffer is the v0 in-memory store; feature
// 004 will swap in internal/adapters/secondary/slogaudit for persistent
// audit output per CLAUDE.md §"Safety".
func (a *Adapter) Record(ctx context.Context, e domain.AuditEvent) error {
	return a.auditBuf.Record(ctx, e)
}
