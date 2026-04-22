package domain

import "fmt"

// GroupPatch is the partial-update payload for GroupAdmin.Edit. Every
// field is a pointer so the caller can distinguish "unchanged" (nil)
// from "set to zero" (non-nil pointing at the zero value).
//
// At least one field MUST be non-nil; Validate enforces this invariant so
// the adapter can route all-nil inputs to -32100 policy_refused.
type GroupPatch struct {
	Subject     *string
	Description *string
	// IconPath is a filesystem path on the daemon host. A pointer to an
	// empty string removes the current icon; a nil pointer leaves it
	// unchanged.
	IconPath *string
}

// Validate enforces that at least one field is non-nil and that string
// values stay within WhatsApp's server-side size limits.
func (p GroupPatch) Validate() error {
	if p.Subject == nil && p.Description == nil && p.IconPath == nil {
		return fmt.Errorf("domain: group patch has no fields set")
	}
	if p.Subject != nil && len(*p.Subject) > maxGroupSubjectBytes {
		return fmt.Errorf("%w: group subject %d > %d bytes", ErrMessageTooLarge, len(*p.Subject), maxGroupSubjectBytes)
	}
	if p.Description != nil && len(*p.Description) > maxGroupDescriptionBytes {
		return fmt.Errorf("%w: group description %d > %d bytes", ErrMessageTooLarge, len(*p.Description), maxGroupDescriptionBytes)
	}
	return nil
}

// maxGroupDescriptionBytes is the WhatsApp server-side limit on group
// descriptions (topic). The official client caps input at 512 bytes.
const maxGroupDescriptionBytes = 512

// GroupInviteLink is the canonical invite-link payload returned by
// GroupAdmin.InviteGet and GroupAdmin.InviteRevoke. URL follows the
// `^https://chat\.whatsapp\.com/[A-Za-z0-9]+$` pattern enforced by the
// adapter.
type GroupInviteLink struct {
	Group JID
	URL   string
	// Code is the trailing token of URL. Extracted here so tests can
	// assert the path segment without re-parsing the URL.
	Code string
}

// IsZero reports whether g holds no data (all three fields zero).
func (g GroupInviteLink) IsZero() bool {
	return g.Group.IsZero() && g.URL == "" && g.Code == ""
}
