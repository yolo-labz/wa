package domain

import (
	"fmt"
	"slices"
	"time"
)

// maxGroupSubjectBytes is the WhatsApp server-side limit on group subjects.
const maxGroupSubjectBytes = 100

// Group represents a WhatsApp group. Admins is a subset of Participants;
// both slices contain addressable JIDs — phone-number user JIDs and LIDs
// (no nested groups). LID participants are accepted because WhatsApp now
// returns LIDs for any contact whose phone number was never disclosed to
// this account (LinkedIn-click-to-WA, business-discovery, group-invite
// joins) — see spec 105.
//
// FR-072 (feature 017 Tier 2): FetchedAt records when the full participant
// roster was last retrieved. Consumers use it with a 60 s TTL to decide
// cache freshness. A zero value means "never fetched" (stale).
type Group struct {
	JID          JID
	Subject      string
	Participants []JID
	Admins       []JID
	FetchedAt    time.Time
}

// NewGroup constructs a Group and validates every invariant.
// Admin tracking is deferred: the returned Group has a nil Admins slice.
func NewGroup(jid JID, subject string, participants []JID) (Group, error) {
	if !jid.IsGroup() {
		return Group{}, fmt.Errorf("%w: %q is not a group JID", ErrInvalidJID, jid.String())
	}
	if len(subject) > maxGroupSubjectBytes {
		return Group{}, fmt.Errorf("%w: group subject %d > %d bytes", ErrMessageTooLarge, len(subject), maxGroupSubjectBytes)
	}
	if len(participants) == 0 {
		return Group{}, fmt.Errorf("%w: group must have at least one participant", ErrInvalidJID)
	}
	for i, p := range participants {
		if !p.IsAddressable() {
			return Group{}, fmt.Errorf("%w: participant[%d]=%q is not an addressable JID", ErrInvalidJID, i, p.String())
		}
	}
	ps := make([]JID, len(participants))
	copy(ps, participants)
	return Group{JID: jid, Subject: subject, Participants: ps}, nil
}

// HasParticipant reports whether j appears in Participants.
func (g Group) HasParticipant(j JID) bool {
	return slices.Contains(g.Participants, j)
}

// IsAdmin reports whether j appears in Admins.
func (g Group) IsAdmin(j JID) bool {
	return slices.Contains(g.Admins, j)
}

// Size returns the number of Participants.
func (g Group) Size() int { return len(g.Participants) }

// IsStale reports whether FetchedAt is older than ttl. Zero FetchedAt is
// always stale. Negative ttl treats every non-zero FetchedAt as fresh
// (used by tests that want to pin clock semantics).
func (g Group) IsStale(now time.Time, ttl time.Duration) bool {
	if g.FetchedAt.IsZero() {
		return true
	}
	if ttl < 0 {
		return false
	}
	return now.Sub(g.FetchedAt) > ttl
}

// WithAdmins returns a copy of g with Admins replaced. Callers must pass
// a JID slice that is a subset of Participants; the check is O(n²) but
// n is bounded by WhatsApp's ~1024 member ceiling.
func (g Group) WithAdmins(admins []JID) (Group, error) {
	for i, a := range admins {
		if !a.IsAddressable() {
			return Group{}, fmt.Errorf("%w: admin[%d]=%q is not an addressable JID", ErrInvalidJID, i, a.String())
		}
		if !g.HasParticipant(a) {
			return Group{}, fmt.Errorf("%w: admin[%d]=%q is not a participant", ErrInvalidJID, i, a.String())
		}
	}
	out := g
	out.Admins = slices.Clone(admins)
	return out, nil
}
