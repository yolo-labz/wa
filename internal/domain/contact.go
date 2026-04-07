package domain

import "fmt"

// Contact represents a WhatsApp contact as known to the daemon.
// PushName is a self-reported name from the network and MUST NOT be
// trusted for any policy decision.
type Contact struct {
	JID      JID
	PushName string
	Verified bool
}

// NewContact constructs a Contact, rejecting a zero JID.
func NewContact(jid JID, pushName string) (Contact, error) {
	if jid.IsZero() {
		return Contact{}, fmt.Errorf("%w: zero JID passed to NewContact", ErrInvalidJID)
	}
	return Contact{JID: jid, PushName: pushName}, nil
}

// IsZero reports whether c is the zero Contact value.
func (c Contact) IsZero() bool { return c.JID.IsZero() && c.PushName == "" && !c.Verified }

// DisplayName returns PushName if non-empty, else the JID's user part.
func (c Contact) DisplayName() string {
	if c.PushName != "" {
		return c.PushName
	}
	return c.JID.User()
}
