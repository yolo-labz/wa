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
	ID       EventID
	TS       time.Time
	From     JID
	Message  Message
	PushName string
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
