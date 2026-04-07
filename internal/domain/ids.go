package domain

// MessageID is an opaque, named-string handle for a WhatsApp message ID.
// It is intentionally NOT a type alias for string so that accidental
// cross-type assignments fail at compile time.
type MessageID string

// String returns the underlying string form of the MessageID.
func (id MessageID) String() string { return string(id) }

// IsZero reports whether the MessageID is the zero value.
func (id MessageID) IsZero() bool { return id == "" }

// EventID is an opaque, named-string handle for a daemon-assigned event
// sequence number.
type EventID string

// String returns the underlying string form of the EventID.
func (id EventID) String() string { return string(id) }

// IsZero reports whether the EventID is the zero value.
func (id EventID) IsZero() bool { return id == "" }
