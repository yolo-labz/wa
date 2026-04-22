package domain

import "time"

// ReceiptStatus enumerates the three WhatsApp delivery receipt states.
type ReceiptStatus uint8

// ReceiptStatus values. Zero is invalid.
const (
	ReceiptDelivered ReceiptStatus = iota + 1
	ReceiptRead
	ReceiptPlayed
)

// String returns the canonical name of the receipt status.
func (s ReceiptStatus) String() string {
	switch s {
	case ReceiptDelivered:
		return "delivered"
	case ReceiptRead:
		return "read"
	case ReceiptPlayed:
		return "played"
	default:
		return "unknown"
	}
}

// IsValid reports whether s is one of the three declared values.
func (s ReceiptStatus) IsValid() bool {
	return s == ReceiptDelivered || s == ReceiptRead || s == ReceiptPlayed
}

// ConnectionState enumerates the daemon-observed websocket states.
type ConnectionState uint8

// ConnectionState values. Zero is invalid.
const (
	ConnDisconnected ConnectionState = iota + 1
	ConnConnecting
	ConnConnected
)

// String returns the canonical name of the connection state.
func (c ConnectionState) String() string {
	switch c {
	case ConnDisconnected:
		return "disconnected"
	case ConnConnecting:
		return "connecting"
	case ConnConnected:
		return "connected"
	default:
		return "unknown"
	}
}

// IsValid reports whether c is one of the three declared values.
func (c ConnectionState) IsValid() bool {
	return c == ConnDisconnected || c == ConnConnecting || c == ConnConnected
}

// PairingState enumerates the pairing-flow states.
type PairingState uint8

// PairingState values. Zero is invalid.
const (
	PairQRCode PairingState = iota + 1
	PairPhoneCode
	PairSuccess
	PairFailure
)

// String returns the canonical name of the pairing state.
func (p PairingState) String() string {
	switch p {
	case PairQRCode:
		return "qr_code"
	case PairPhoneCode:
		return "phone_code"
	case PairSuccess:
		return "success"
	case PairFailure:
		return "failure"
	default:
		return "unknown"
	}
}

// IsValid reports whether p is one of the four declared values.
func (p PairingState) IsValid() bool {
	return p == PairQRCode || p == PairPhoneCode || p == PairSuccess || p == PairFailure
}

// Event is the sealed sum type for inbound events delivered by the
// EventStream port. Only the four variants declared in this file may
// satisfy it.
type Event interface {
	isEvent()
	EventID() EventID
	Timestamp() time.Time
}

// MessageEvent is an inbound WhatsApp message.
type MessageEvent struct {
	ID          EventID
	TS          time.Time
	From        JID
	Message     Message
	PushName    string
	Quoted      *QuotedMessage      // FR-012: set when sender quoted a prior message
	Interactive *InteractivePayload // FR-130: set for list/button reply messages
}

// isEvent implements the sealed Event interface marker.
func (MessageEvent) isEvent() { /* sealed interface marker — intentionally empty */ }

// EventID returns the event's unique id.
func (e MessageEvent) EventID() EventID { return e.ID }

// Timestamp returns the event's observed timestamp.
func (e MessageEvent) Timestamp() time.Time { return e.TS }

// ReceiptEvent is a delivery/read receipt for a previously sent message.
type ReceiptEvent struct {
	ID        EventID
	TS        time.Time
	Chat      JID
	MessageID MessageID
	Status    ReceiptStatus
}

// isEvent implements the sealed Event interface marker.
func (ReceiptEvent) isEvent() { /* sealed interface marker — intentionally empty */ }

// EventID returns the event's unique id.
func (e ReceiptEvent) EventID() EventID { return e.ID }

// Timestamp returns the event's observed timestamp.
func (e ReceiptEvent) Timestamp() time.Time { return e.TS }

// ConnectionEvent reports a websocket state transition.
type ConnectionEvent struct {
	ID    EventID
	TS    time.Time
	State ConnectionState
}

// isEvent implements the sealed Event interface marker.
func (ConnectionEvent) isEvent() { /* sealed interface marker — intentionally empty */ }

// EventID returns the event's unique id.
func (e ConnectionEvent) EventID() EventID { return e.ID }

// Timestamp returns the event's observed timestamp.
func (e ConnectionEvent) Timestamp() time.Time { return e.TS }

// PairingEvent reports a pairing-flow state transition.
type PairingEvent struct {
	ID    EventID
	TS    time.Time
	State PairingState
	Code  string
}

// isEvent implements the sealed Event interface marker.
func (PairingEvent) isEvent() { /* sealed interface marker — intentionally empty */ }

// EventID returns the event's unique id.
func (e PairingEvent) EventID() EventID { return e.ID }

// Timestamp returns the event's observed timestamp.
func (e PairingEvent) Timestamp() time.Time { return e.TS }

// QuotedMessage is the value object embedded in a MessageEvent when the
// sender quoted a prior message. Body is attacker-controllable untrusted
// text; consumers MUST treat it as hostile per the injection firewall.
type QuotedMessage struct {
	MessageID   MessageID
	ChatJID     JID
	SenderJID   JID
	BodyPreview string // truncated to QuotedBodyPreviewMax chars
	TS          time.Time
}

// QuotedBodyPreviewMax is the character ceiling for QuotedMessage.BodyPreview.
const QuotedBodyPreviewMax = 200

// InteractiveSubtype names the interactive-message kinds surfaced inbound
// (FR-130). Outbound interactive messages are explicitly out of scope for
// v0.5 (FR-131).
type InteractiveSubtype uint8

// InteractiveSubtype values. Zero is invalid.
const (
	InteractiveList InteractiveSubtype = iota + 1
	InteractiveButtons
)

// String returns the canonical lowercase name used on the wire.
func (s InteractiveSubtype) String() string {
	switch s {
	case InteractiveList:
		return "list"
	case InteractiveButtons:
		return "buttons"
	default:
		return "unknown"
	}
}

// IsValid reports whether s is one of the two declared subtypes.
func (s InteractiveSubtype) IsValid() bool {
	return s == InteractiveList || s == InteractiveButtons
}

// InteractiveOption is one selectable item in a list or button reply.
type InteractiveOption struct {
	ID    string
	Label string
}

// InteractivePayload is the value object embedded in a MessageEvent when
// whatsmeow delivered a list/button reply (FR-130). Prompt and Option
// labels are attacker-controllable untrusted strings.
type InteractivePayload struct {
	Subtype InteractiveSubtype
	Prompt  string
	Options []InteractiveOption
}

// EditEvent reports an upstream edit to a previously-delivered message
// (FR-013). The new body replaces the original in the thread view.
type EditEvent struct {
	ID         EventID
	TS         time.Time
	Chat       JID
	Sender     JID
	OriginalID MessageID
	NewBody    string
	EditedAt   time.Time
}

// isEvent implements the sealed Event interface marker.
func (EditEvent) isEvent() { /* sealed interface marker — intentionally empty */ }

// EventID returns the event's unique id.
func (e EditEvent) EventID() EventID { return e.ID }

// Timestamp returns the event's observed timestamp.
func (e EditEvent) Timestamp() time.Time { return e.TS }

// RevokeEvent reports an upstream revoke ("delete for everyone") of a
// message (FR-013). The revoked message is tombstoned rather than deleted
// so thread history remains cursorable.
type RevokeEvent struct {
	ID         EventID
	TS         time.Time
	Chat       JID
	Sender     JID
	OriginalID MessageID
}

// isEvent implements the sealed Event interface marker.
func (RevokeEvent) isEvent() { /* sealed interface marker — intentionally empty */ }

// EventID returns the event's unique id.
func (e RevokeEvent) EventID() EventID { return e.ID }

// Timestamp returns the event's observed timestamp.
func (e RevokeEvent) Timestamp() time.Time { return e.TS }

// StreamDropEvent is a synthetic event surfaced by EventStream when the
// bounded event buffer overflowed or a ResumeFrom request could not be
// satisfied from the ring. It is the visible signal that some real
// events between FromSeq and ToSeq (inclusive) were not delivered —
// subscribers that care about completeness MUST reload from history.
// Feature 018 T1-13 / R-04 — CLAUDE.md rule 12 (no silent fallbacks).
type StreamDropEvent struct {
	ID           EventID
	TS           time.Time
	FromSeq      uint64
	ToSeq        uint64
	DroppedCount uint64
	Reason       string
}

// isEvent implements the sealed Event interface marker.
func (StreamDropEvent) isEvent() { /* sealed interface marker — intentionally empty */ }

// EventID returns the event's unique id.
func (e StreamDropEvent) EventID() EventID { return e.ID }

// Timestamp returns the event's observed timestamp.
func (e StreamDropEvent) Timestamp() time.Time { return e.TS }

// InboundReactionEvent is a reaction another party attached to a message
// in a chat the daemon observes. Empty Emoji means the reactor removed
// their prior reaction.
type InboundReactionEvent struct {
	ID       EventID
	TS       time.Time
	Chat     JID
	Reactor  JID
	TargetID MessageID
	Emoji    string
}

// isEvent implements the sealed Event interface marker.
func (InboundReactionEvent) isEvent() { /* sealed interface marker — intentionally empty */ }

// EventID returns the event's unique id.
func (e InboundReactionEvent) EventID() EventID { return e.ID }

// Timestamp returns the event's observed timestamp.
func (e InboundReactionEvent) Timestamp() time.Time { return e.TS }
