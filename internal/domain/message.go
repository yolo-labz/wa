package domain

import "fmt"

// Size limits for message bodies. MaxMediaBytes is the domain's
// *constraint*; the filesystem-size check is delegated to the adapter
// because the domain has no os import.
const (
	MaxTextBytes  = 64 * 1024
	MaxMediaBytes = 16 * 1024 * 1024
)

// Message is the sealed sum type for outbound messages. Only the three
// variants declared in this file may satisfy it, because isMessage() is
// an unexported sentinel method.
type Message interface {
	isMessage()
	To() JID
	Validate() error
}

// TextMessage is a plain-text outbound message.
type TextMessage struct {
	Recipient   JID
	Body        string
	LinkPreview bool
}

// isMessage implements the sealed Message interface marker.
func (TextMessage) isMessage() { /* sealed interface marker — intentionally empty */ }

// To returns the recipient JID.
func (m TextMessage) To() JID { return m.Recipient }

// Validate enforces: non-zero recipient, non-empty body, body ≤ MaxTextBytes.
func (m TextMessage) Validate() error {
	if m.Recipient.IsZero() {
		return fmt.Errorf("%w: TextMessage has zero recipient", ErrInvalidJID)
	}
	if m.Body == "" {
		return fmt.Errorf("%w: TextMessage has empty body", ErrEmptyBody)
	}
	if len(m.Body) > MaxTextBytes {
		return fmt.Errorf("%w: TextMessage body %d > %d bytes", ErrMessageTooLarge, len(m.Body), MaxTextBytes)
	}
	return nil
}

// MediaMessage is an outbound media (file path) message. Size checking of
// the file itself happens in the adapter that performs the os.Stat call.
type MediaMessage struct {
	Recipient JID
	Path      string
	Mime      string
	Caption   string
}

// isMessage implements the sealed Message interface marker.
func (MediaMessage) isMessage() { /* sealed interface marker — intentionally empty */ }

// To returns the recipient JID.
func (m MediaMessage) To() JID { return m.Recipient }

// Validate enforces: non-zero recipient, non-empty path, non-empty mime.
func (m MediaMessage) Validate() error {
	if m.Recipient.IsZero() {
		return fmt.Errorf("%w: MediaMessage has zero recipient", ErrInvalidJID)
	}
	if m.Path == "" {
		return fmt.Errorf("%w: MediaMessage has empty path", ErrEmptyBody)
	}
	if m.Mime == "" {
		return fmt.Errorf("%w: MediaMessage has empty mime", ErrEmptyBody)
	}
	return nil
}

// ReactionMessage is an outbound emoji reaction. An empty Emoji is the
// valid "remove reaction" sentinel per the WhatsApp protocol.
type ReactionMessage struct {
	Recipient JID
	TargetID  MessageID
	Emoji     string
}

// isMessage implements the sealed Message interface marker.
func (ReactionMessage) isMessage() { /* sealed interface marker — intentionally empty */ }

// To returns the recipient JID (the chat the target message lives in).
func (m ReactionMessage) To() JID { return m.Recipient }

// Validate enforces: non-zero recipient, non-zero target. Empty emoji is
// allowed and means "remove the reaction".
func (m ReactionMessage) Validate() error {
	if m.Recipient.IsZero() {
		return fmt.Errorf("%w: ReactionMessage has zero recipient", ErrInvalidJID)
	}
	if m.TargetID.IsZero() {
		return fmt.Errorf("%w: ReactionMessage has zero target", ErrEmptyBody)
	}
	return nil
}
