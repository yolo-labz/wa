package app

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/yolo-labz/wa/v2/internal/domain"
)

// contactsResolveConfirmParams is the JSON-RPC params for
// "contacts.resolve.confirm" (FR-026). e164 is the raw phone string; the
// adapter normalises it via domain.ParsePhone and queries upstream.
//
// The "confirm" suffix reflects the two-step flow: the caller (plugin or
// CLI user) has already obtained explicit user opt-in for the lookup
// before invoking this method. The wire method itself does not carry a
// separate handle — it is the confirmation step.
type contactsResolveConfirmParams struct {
	E164 string `json:"e164"`
}

// contactsResolveConfirmResult is the JSON-RPC result for
// "contacts.resolve.confirm". A bare {jid} shape avoids leaking upstream
// metadata the client did not request.
type contactsResolveConfirmResult struct {
	JID string `json:"jid"`
}

// handleContactsResolveConfirm implements "contacts.resolve.confirm"
// (FR-026). Read-only — no idempotency sidecar. The adapter is expected
// to short-circuit "this number is not on WhatsApp" into a typed error
// (domain.ErrNotFound style) so the socket layer can map to -32101.
func (d *Dispatcher) handleContactsResolveConfirm(ctx context.Context, raw json.RawMessage) (json.RawMessage, error) {
	var p contactsResolveConfirmParams
	if err := parseParams(raw, &p); err != nil {
		return nil, err
	}
	if p.E164 == "" {
		return nil, ErrInvalidParams
	}
	if _, err := domain.ParsePhone(p.E164); err != nil {
		return nil, ErrInvalidParams
	}
	jid, err := d.contacts.Resolve(ctx, p.E164)
	if err != nil {
		return nil, fmt.Errorf("contacts.resolve.confirm: %w", err)
	}
	return marshalResult(contactsResolveConfirmResult{JID: jid.String()})
}

// RefreshPushName is the use case that keeps the local contact mirror in
// sync with inbound message pushName metadata (FR-028). It is invoked by
// the whatsmeow adapter from persistInboundMessage via a function hook
// wired at composition time — the adapter itself has no ContactSearcher
// dependency so keeping the policy in the app layer preserves the
// hexagonal boundary (CLAUDE.md rule 23).
//
// No-op when contacts is not a ContactSearcher (v0 adapter without a
// local mirror), jid is zero, or pushName is empty. The Lookup →
// comparison → Upsert path avoids write amplification on the common
// "pushName unchanged" case.
func (d *Dispatcher) RefreshPushName(ctx context.Context, jid domain.JID, pushName string) {
	if jid.IsZero() || pushName == "" {
		return
	}
	searcher, ok := d.contacts.(ContactSearcher)
	if !ok {
		return
	}
	existing, lookErr := d.contacts.Lookup(ctx, jid)
	if lookErr == nil && existing.PushName == pushName {
		return
	}
	_ = searcher.Upsert(ctx, domain.Contact{JID: jid, PushName: pushName})
}
