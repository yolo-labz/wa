package app

import (
	"context"

	"github.com/yolo-labz/wa/v2/internal/domain"
)

// IdentityResolver resolves between phone-number JIDs (`@s.whatsapp.net`)
// and LIDs (`@lid`, the WhatsApp HiddenUserServer namespace). LIDs are
// returned by WhatsApp in place of phone numbers whenever the contact's
// PN was never disclosed to this account — LinkedIn-click-to-WA deep
// links, business discovery, group joins by invite, and as the new
// default identity for fresh Multi-Device sessions. See spec 105 for
// the namespace boundary; this port is the spec 106 follow-up that
// surfaces the mapping at the use-case layer.
//
// Implementations:
//   - whatsmeow secondary adapter delegates to `Client.Store.LIDs`
//     (a `whatsmeow/store.LIDStore` instance).
//   - in-memory stub keeps a deterministic two-way map.
//
// Resolution semantics: a successful call MAY legitimately return the
// zero JID with `nil` err when no mapping is known yet — most LIDs do
// not have a PN counterpart from this account's perspective until the
// contact replies and shares it. Callers MUST NOT treat zero as an
// error; instead, surface the absence to the user as "PN not yet
// known" so they can decide whether to wait or proceed via the LID.
//
// All methods MUST be safe for concurrent use.
type IdentityResolver interface {
	// ResolveLID returns the LID associated with pn, or the zero JID
	// (with nil err) if no mapping is known. pn MUST satisfy
	// `IsUser()`. Returns ErrNotIdentity if pn is not a PN JID.
	ResolveLID(ctx context.Context, pn domain.JID) (domain.JID, error)

	// ResolvePN returns the PN associated with lid, or the zero JID
	// (with nil err) if no mapping is known. lid MUST satisfy
	// `IsLID()`. Returns ErrNotIdentity if lid is not a LID.
	ResolvePN(ctx context.Context, lid domain.JID) (domain.JID, error)

	// RecordMapping stores a (pn, lid) pair. Called by adapters that
	// surface mappings out-of-band (e.g. inbound message processing in
	// whatsmeow). Both arguments MUST be non-zero and of the right
	// kind; otherwise returns ErrNotIdentity. Repeated calls with the
	// same pair are idempotent.
	RecordMapping(ctx context.Context, pn, lid domain.JID) error
}
