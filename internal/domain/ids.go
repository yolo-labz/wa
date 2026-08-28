package domain

// MessageID is an opaque, named-string handle for a WhatsApp message ID.
// It is intentionally NOT a type alias for string so that accidental
// cross-type assignments fail at compile time.
type MessageID string

// String returns the underlying string form of the MessageID.
func (id MessageID) String() string { return string(id) }

// IsZero reports whether the MessageID is the zero value.
func (id MessageID) IsZero() bool { return id == "" }

// SafeMessageIDMax bounds a stanza id at twice the longest form WhatsApp
// is known to emit (32 hex chars), so a real id can grow without this
// constant moving while an id-shaped injection payload cannot.
const SafeMessageIDMax = 64

// IsSafe reports whether id is shaped like a WhatsApp stanza id and is
// therefore presentable to a subscriber as a plain, structural field.
//
// This is a trust boundary, not a format check. A stanza id arrives as
// the raw `id` attribute of an inbound message stanza — whatsmeow assigns
// it verbatim and does not validate it (`parseMessageInfo` does
// `info.ID = types.MessageID(ag.String("id"))`, and `types.MessageID` is
// a bare `= string` alias), so the sending device chooses every byte. An
// id is only ever a correlation handle, so the character set it needs is
// tiny: alphanumerics plus the punctuation real ids use (hex, base64 with
// padding, and the `@`/`:` seen on business and newsletter ids). Refusing
// everything else keeps prose, markup, quotes, whitespace and control
// bytes out of a field consumers read as trusted structure.
func (id MessageID) IsSafe() bool {
	if len(id) == 0 || len(id) > SafeMessageIDMax {
		return false
	}
	for i := range len(id) {
		switch c := id[i]; {
		case c >= 'A' && c <= 'Z', c >= 'a' && c <= 'z', c >= '0' && c <= '9':
		case c == '-', c == '_', c == '.', c == ':', c == '@', c == '=', c == '+', c == '/':
		default:
			return false
		}
	}
	return true
}

// EventID is an opaque, named-string handle for a daemon-assigned event
// sequence number.
type EventID string

// String returns the underlying string form of the EventID.
func (id EventID) String() string { return string(id) }

// IsZero reports whether the EventID is the zero value.
func (id EventID) IsZero() bool { return id == "" }
