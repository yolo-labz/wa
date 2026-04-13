package domain

import "slices"

import "fmt"

// maxGroupSubjectBytes is the WhatsApp server-side limit on group subjects.
const maxGroupSubjectBytes = 100

// Group represents a WhatsApp group. Admins is a subset of Participants;
// both slices contain user JIDs only (no nested groups).
type Group struct {
	JID          JID
	Subject      string
	Participants []JID
	Admins       []JID
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
		if !p.IsUser() {
			return Group{}, fmt.Errorf("%w: participant[%d]=%q is not a user JID", ErrInvalidJID, i, p.String())
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
