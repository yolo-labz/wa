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

// AudioMessage is an inbound-or-outbound audio/voice-note variant.
// Feature 018 T1-12 / R-05 — the event translator now emits this instead
// of silently flattening voice notes into an empty TextMessage. Outbound
// send is not yet wired (the builder will land in Tier 2); Validate
// still enforces the minimum shape so any future send path is honest.
type AudioMessage struct {
	Recipient JID
	Path      string
	Mime      string
	Seconds   int
	PTT       bool
}

func (AudioMessage) isMessage()  { /* sealed interface marker — intentionally empty */ }
func (m AudioMessage) To() JID   { return m.Recipient }
func (m AudioMessage) Validate() error {
	if m.Recipient.IsZero() {
		return fmt.Errorf("%w: AudioMessage has zero recipient", ErrInvalidJID)
	}
	if m.Path == "" {
		return fmt.Errorf("%w: AudioMessage has empty path", ErrEmptyBody)
	}
	return nil
}

// VideoMessage mirrors AudioMessage for video payloads. Caption may be
// empty (the protocol allows captionless videos).
type VideoMessage struct {
	Recipient JID
	Path      string
	Mime      string
	Caption   string
	Seconds   int
	IsGif     bool
}

func (VideoMessage) isMessage()  { /* sealed interface marker — intentionally empty */ }
func (m VideoMessage) To() JID   { return m.Recipient }
func (m VideoMessage) Validate() error {
	if m.Recipient.IsZero() {
		return fmt.Errorf("%w: VideoMessage has zero recipient", ErrInvalidJID)
	}
	if m.Path == "" {
		return fmt.Errorf("%w: VideoMessage has empty path", ErrEmptyBody)
	}
	return nil
}

// DocumentMessage carries a generic file with an explicit filename the
// recipient sees in the attachment UI.
type DocumentMessage struct {
	Recipient JID
	Path      string
	Mime      string
	Filename  string
	Caption   string
}

func (DocumentMessage) isMessage()  { /* sealed interface marker — intentionally empty */ }
func (m DocumentMessage) To() JID   { return m.Recipient }
func (m DocumentMessage) Validate() error {
	if m.Recipient.IsZero() {
		return fmt.Errorf("%w: DocumentMessage has zero recipient", ErrInvalidJID)
	}
	if m.Path == "" {
		return fmt.Errorf("%w: DocumentMessage has empty path", ErrEmptyBody)
	}
	return nil
}

// StickerMessage is a static or animated WebP sticker.
type StickerMessage struct {
	Recipient  JID
	Path       string
	Mime       string
	IsAnimated bool
}

func (StickerMessage) isMessage()  { /* sealed interface marker — intentionally empty */ }
func (m StickerMessage) To() JID   { return m.Recipient }
func (m StickerMessage) Validate() error {
	if m.Recipient.IsZero() {
		return fmt.Errorf("%w: StickerMessage has zero recipient", ErrInvalidJID)
	}
	if m.Path == "" {
		return fmt.Errorf("%w: StickerMessage has empty path", ErrEmptyBody)
	}
	return nil
}

// ContactCard is a shared vCard. DisplayName is what WhatsApp shows in
// the message bubble; VCard is the raw RFC 6350 payload.
type ContactCard struct {
	Recipient   JID
	DisplayName string
	VCard       string
}

func (ContactCard) isMessage()  { /* sealed interface marker — intentionally empty */ }
func (m ContactCard) To() JID   { return m.Recipient }
func (m ContactCard) Validate() error {
	if m.Recipient.IsZero() {
		return fmt.Errorf("%w: ContactCard has zero recipient", ErrInvalidJID)
	}
	if m.VCard == "" {
		return fmt.Errorf("%w: ContactCard has empty vcard", ErrEmptyBody)
	}
	return nil
}

// LocationPin is a shared coordinate. Name and Address are optional;
// lat/lon are required and follow WGS84 degrees.
type LocationPin struct {
	Recipient JID
	Latitude  float64
	Longitude float64
	Name      string
	Address   string
}

func (LocationPin) isMessage()  { /* sealed interface marker — intentionally empty */ }
func (m LocationPin) To() JID   { return m.Recipient }
func (m LocationPin) Validate() error {
	if m.Recipient.IsZero() {
		return fmt.Errorf("%w: LocationPin has zero recipient", ErrInvalidJID)
	}
	if m.Latitude < -90 || m.Latitude > 90 {
		return fmt.Errorf("%w: LocationPin latitude %f out of range", ErrEmptyBody, m.Latitude)
	}
	if m.Longitude < -180 || m.Longitude > 180 {
		return fmt.Errorf("%w: LocationPin longitude %f out of range", ErrEmptyBody, m.Longitude)
	}
	return nil
}

// UnknownMessage is the last-resort variant used when the inbound event
// translator recognises a top-level waE2E.Message oneof branch it does
// not yet map onto a typed domain variant. Its Detail field carries a
// short descriptor (`imageMessage`, `protocolMessage:3`, …) so downstream
// consumers and audit logs can see what was dropped instead of swallowing
// the event silently. Feature 018 T1-12 / R-05 — "no silent fallbacks".
//
// Validate rejects zero-recipient by the same rule as every other
// variant, but does NOT require Detail to be non-empty: a truly unknown
// event still carries the recipient and is better than a zero-body
// TextMessage placeholder.
type UnknownMessage struct {
	Recipient JID
	Detail    string
}

func (UnknownMessage) isMessage()  { /* sealed interface marker — intentionally empty */ }
func (m UnknownMessage) To() JID   { return m.Recipient }
func (m UnknownMessage) Validate() error {
	if m.Recipient.IsZero() {
		return fmt.Errorf("%w: UnknownMessage has zero recipient", ErrInvalidJID)
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
