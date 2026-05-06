package domain

import (
	"fmt"
	"log/slog"
	"time"
)

// Session is the domain's opaque handle for a paired WhatsApp session.
// The actual Signal Protocol material (prekeys, ratchets, registration
// id) lives inside the secondary adapter, not here.
type Session struct {
	jid       JID
	deviceID  uint16
	createdAt time.Time
}

// NewSession constructs a Session. jid must be non-zero and deviceID must
// be > 0.
func NewSession(jid JID, deviceID uint16, createdAt time.Time) (Session, error) {
	if jid.IsZero() {
		return Session{}, fmt.Errorf("%w: NewSession requires a non-zero JID", ErrInvalidJID)
	}
	if deviceID == 0 {
		return Session{}, fmt.Errorf("%w: NewSession requires a non-zero deviceID", ErrInvalidSession)
	}
	return Session{jid: jid, deviceID: deviceID, createdAt: createdAt}, nil
}

// JID returns the session's JID.
func (s Session) JID() JID { return s.jid }

// DeviceID returns the session's device id.
func (s Session) DeviceID() uint16 { return s.deviceID }

// CreatedAt returns the session's creation timestamp.
func (s Session) CreatedAt() time.Time { return s.createdAt }

// IsZero reports whether s is the zero Session value.
func (s Session) IsZero() bool {
	return s.jid.IsZero() && s.deviceID == 0 && s.createdAt.IsZero()
}

// IsLoggedIn reports whether s represents an active paired session.
func (s Session) IsLoggedIn() bool {
	return !s.jid.IsZero() && s.deviceID > 0
}

// LogValue implements slog.LogValuer with deliberately narrow output.
// Spec 016 FR-013 / T069.
//
// The domain Session is already opaque (the Signal-Protocol prekeys,
// ratchets, identity keys, and registration id all live INSIDE the
// secondary adapter, never here) so there is no plaintext secret in
// this struct to redact. What we log is the public-information set
// only: the JID, the device id, and the createdAt timestamp. The
// reason this method exists despite that is to (a) opt out of the
// reflect-based default that exposes the unexported field names, and
// (b) keep the log shape stable as future fields are added — any new
// field MUST be reviewed against this method and either added here
// (public) or omitted (sensitive).
func (s Session) LogValue() slog.Value {
	return slog.GroupValue(
		slog.Any("jid", s.jid),
		slog.Int64("deviceId", int64(s.deviceID)),
		slog.Time("createdAt", s.createdAt),
	)
}
