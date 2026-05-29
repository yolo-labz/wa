package domain

import (
	"fmt"
	"log/slog"
)

// previewLen caps the truncated message body included in log lines.
// Long enough to disambiguate two messages, short enough to keep log
// lines greppable. Spec 016 FR-013 / T068.
const previewLen = 32

// preview returns body truncated to previewLen characters with a
// trailing ellipsis when truncation occurred. Always safe on UTF-8
// inputs because the truncation is byte-counted and slog.StringValue
// is byte-safe; downstream JSON encoders re-validate.
func preview(body string) string {
	if len(body) <= previewLen {
		return body
	}
	return body[:previewLen] + "…"
}

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

// LogValue implements slog.LogValuer so a TextMessage logs with its
// recipient + size + truncated body preview rather than the full raw
// body (which can be 64 KiB). Spec 016 FR-013 / T068.
func (m TextMessage) LogValue() slog.Value {
	return slog.GroupValue(
		slog.String("type", "text"),
		slog.Any("to", m.Recipient),
		slog.Int("bytes", len(m.Body)),
		slog.String("preview", preview(m.Body)),
	)
}

// MediaMessage is an outbound media message. The payload may be sourced
// three mutually-exclusive ways (spec 197 — the "media byte seam"):
//
//   - Path: a file on the DAEMON's filesystem. The original, back-compatible
//     source; size checking of the file itself happens in the adapter that
//     performs the os.Stat call.
//   - Bytes: the raw plaintext inlined by the caller. Lets a REMOTE client
//     send a file the daemon cannot see on disk. Capped at MaxMediaBytes.
//   - SHA256: a lowercase-hex sha256 handle for an object already in the
//     daemon's media store. Lets a remote client reference content the
//     daemon already holds without re-uploading the bytes.
//
// EXACTLY ONE of the three MUST be set; SourceValidate enforces the XOR.
// The zero value (Path-only callers leaving Bytes/SHA256 unset) is
// unchanged, so existing senders keep working byte-for-byte.
type MediaMessage struct {
	Recipient JID
	Path      string
	Mime      string
	Caption   string

	// Bytes is the inline plaintext payload (mutually exclusive with Path
	// and SHA256). When set, len(Bytes) MUST be ≤ MaxMediaBytes.
	Bytes []byte
	// SHA256 is the lowercase-hex sha256 handle of an object already in the
	// media store (mutually exclusive with Path and Bytes).
	SHA256 string
}

// isMessage implements the sealed Message interface marker.
func (MediaMessage) isMessage() { /* sealed interface marker — intentionally empty */ }

// To returns the recipient JID.
func (m MediaMessage) To() JID { return m.Recipient }

// Validate enforces: non-zero recipient, exactly one payload source
// (Path XOR Bytes XOR SHA256), non-empty mime. Source-selection rules
// live in SourceValidate so callers that only need the source check (the
// app-layer param decoder) can reuse it without the recipient/mime gates.
func (m MediaMessage) Validate() error {
	if m.Recipient.IsZero() {
		return fmt.Errorf("%w: MediaMessage has zero recipient", ErrInvalidJID)
	}
	if err := m.SourceValidate(); err != nil {
		return err
	}
	if m.Mime == "" {
		return fmt.Errorf("%w: MediaMessage has empty mime", ErrEmptyBody)
	}
	return nil
}

// SourceValidate enforces the payload-source invariant independent of the
// recipient/mime gates: EXACTLY ONE of Path, Bytes, SHA256 is set, and an
// inline Bytes payload is within MaxMediaBytes. It is the constructor-style
// check the app layer runs against caller-supplied params before a JID even
// exists, and Validate() reuses it so the rule has a single home.
func (m MediaMessage) SourceValidate() error {
	sources := 0
	if m.Path != "" {
		sources++
	}
	if len(m.Bytes) > 0 {
		sources++
	}
	if m.SHA256 != "" {
		sources++
	}
	switch {
	case sources == 0:
		return fmt.Errorf("%w: MediaMessage has no payload source (set exactly one of path, bytes, sha256)", ErrEmptyBody)
	case sources > 1:
		return fmt.Errorf("%w: MediaMessage has %d payload sources (set exactly one of path, bytes, sha256)", ErrEmptyBody, sources)
	}
	if int64(len(m.Bytes)) > MaxMediaBytes {
		return fmt.Errorf("%w: MediaMessage inline bytes %d > %d", ErrMessageTooLarge, len(m.Bytes), MaxMediaBytes)
	}
	return nil
}

// LogValue — see TextMessage.LogValue. The raw inline payload is never
// logged; only its source kind and (for bytes) length are surfaced.
func (m MediaMessage) LogValue() slog.Value {
	source := "none"
	switch {
	case m.Path != "":
		source = "path"
	case len(m.Bytes) > 0:
		source = "bytes"
	case m.SHA256 != "":
		source = "sha256"
	}
	return slog.GroupValue(
		slog.String("type", "media"),
		slog.Any("to", m.Recipient),
		slog.String("mime", m.Mime),
		slog.String("source", source),
		slog.Int("bytes", len(m.Bytes)),
		slog.String("preview", preview(m.Caption)),
	)
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

// isMessage implements the sealed Message interface marker.
func (AudioMessage) isMessage() { /* sealed interface marker — intentionally empty */ }

// To returns the recipient JID.
func (m AudioMessage) To() JID { return m.Recipient }

// Validate enforces: non-zero recipient, non-empty path.
func (m AudioMessage) Validate() error {
	if m.Recipient.IsZero() {
		return fmt.Errorf("%w: AudioMessage has zero recipient", ErrInvalidJID)
	}
	if m.Path == "" {
		return fmt.Errorf("%w: AudioMessage has empty path", ErrEmptyBody)
	}
	return nil
}

// LogValue — see TextMessage.LogValue.
func (m AudioMessage) LogValue() slog.Value {
	return slog.GroupValue(
		slog.String("type", "audio"),
		slog.Any("to", m.Recipient),
		slog.Int("seconds", m.Seconds),
		slog.Bool("ptt", m.PTT),
	)
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

// isMessage implements the sealed Message interface marker.
func (VideoMessage) isMessage() { /* sealed interface marker — intentionally empty */ }

// To returns the recipient JID.
func (m VideoMessage) To() JID { return m.Recipient }

// Validate enforces: non-zero recipient, non-empty path.
func (m VideoMessage) Validate() error {
	if m.Recipient.IsZero() {
		return fmt.Errorf("%w: VideoMessage has zero recipient", ErrInvalidJID)
	}
	if m.Path == "" {
		return fmt.Errorf("%w: VideoMessage has empty path", ErrEmptyBody)
	}
	return nil
}

// LogValue — see TextMessage.LogValue.
func (m VideoMessage) LogValue() slog.Value {
	return slog.GroupValue(
		slog.String("type", "video"),
		slog.Any("to", m.Recipient),
		slog.Int("seconds", m.Seconds),
		slog.Bool("gif", m.IsGif),
		slog.String("preview", preview(m.Caption)),
	)
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

// isMessage implements the sealed Message interface marker.
func (DocumentMessage) isMessage() { /* sealed interface marker — intentionally empty */ }

// To returns the recipient JID.
func (m DocumentMessage) To() JID { return m.Recipient }

// Validate enforces: non-zero recipient, non-empty path.
func (m DocumentMessage) Validate() error {
	if m.Recipient.IsZero() {
		return fmt.Errorf("%w: DocumentMessage has zero recipient", ErrInvalidJID)
	}
	if m.Path == "" {
		return fmt.Errorf("%w: DocumentMessage has empty path", ErrEmptyBody)
	}
	return nil
}

// LogValue — see TextMessage.LogValue.
func (m DocumentMessage) LogValue() slog.Value {
	return slog.GroupValue(
		slog.String("type", "document"),
		slog.Any("to", m.Recipient),
		slog.String("filename", m.Filename),
		slog.String("mime", m.Mime),
	)
}

// StickerMessage is a static or animated WebP sticker.
type StickerMessage struct {
	Recipient  JID
	Path       string
	Mime       string
	IsAnimated bool
}

// isMessage implements the sealed Message interface marker.
func (StickerMessage) isMessage() { /* sealed interface marker — intentionally empty */ }

// To returns the recipient JID.
func (m StickerMessage) To() JID { return m.Recipient }

// Validate enforces: non-zero recipient, non-empty path.
func (m StickerMessage) Validate() error {
	if m.Recipient.IsZero() {
		return fmt.Errorf("%w: StickerMessage has zero recipient", ErrInvalidJID)
	}
	if m.Path == "" {
		return fmt.Errorf("%w: StickerMessage has empty path", ErrEmptyBody)
	}
	return nil
}

// LogValue — see TextMessage.LogValue.
func (m StickerMessage) LogValue() slog.Value {
	return slog.GroupValue(
		slog.String("type", "sticker"),
		slog.Any("to", m.Recipient),
		slog.Bool("animated", m.IsAnimated),
	)
}

// ContactCard is a shared vCard. DisplayName is what WhatsApp shows in
// the message bubble; VCard is the raw RFC 6350 payload.
type ContactCard struct {
	Recipient   JID
	DisplayName string
	VCard       string
}

// isMessage implements the sealed Message interface marker.
func (ContactCard) isMessage() { /* sealed interface marker — intentionally empty */ }

// To returns the recipient JID.
func (m ContactCard) To() JID { return m.Recipient }

// Validate enforces: non-zero recipient, non-empty vcard.
func (m ContactCard) Validate() error {
	if m.Recipient.IsZero() {
		return fmt.Errorf("%w: ContactCard has zero recipient", ErrInvalidJID)
	}
	if m.VCard == "" {
		return fmt.Errorf("%w: ContactCard has empty vcard", ErrEmptyBody)
	}
	return nil
}

// LogValue — see TextMessage.LogValue.
func (m ContactCard) LogValue() slog.Value {
	return slog.GroupValue(
		slog.String("type", "contact"),
		slog.Any("to", m.Recipient),
		slog.String("name", m.DisplayName),
	)
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

// isMessage implements the sealed Message interface marker.
func (LocationPin) isMessage() { /* sealed interface marker — intentionally empty */ }

// To returns the recipient JID.
func (m LocationPin) To() JID { return m.Recipient }

// Validate enforces: non-zero recipient, latitude in [-90, 90], longitude in [-180, 180].
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

// LogValue — see TextMessage.LogValue. Coordinates are intentionally
// included; if a deployment treats location as PII the slog handler
// can register a LogValuer wrapper that suppresses these fields.
func (m LocationPin) LogValue() slog.Value {
	return slog.GroupValue(
		slog.String("type", "location"),
		slog.Any("to", m.Recipient),
		slog.Float64("lat", m.Latitude),
		slog.Float64("lon", m.Longitude),
	)
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

// isMessage implements the sealed Message interface marker.
func (UnknownMessage) isMessage() { /* sealed interface marker — intentionally empty */ }

// To returns the recipient JID.
func (m UnknownMessage) To() JID { return m.Recipient }

// Validate enforces: non-zero recipient. Detail is intentionally not required
// per T1-12 / R-05 — a truly unknown event is better surfaced than silently
// flattened.
func (m UnknownMessage) Validate() error {
	if m.Recipient.IsZero() {
		return fmt.Errorf("%w: UnknownMessage has zero recipient", ErrInvalidJID)
	}
	return nil
}

// LogValue — see TextMessage.LogValue.
func (m UnknownMessage) LogValue() slog.Value {
	return slog.GroupValue(
		slog.String("type", "unknown"),
		slog.Any("to", m.Recipient),
		slog.String("detail", m.Detail),
	)
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

// LogValue — see TextMessage.LogValue.
func (m ReactionMessage) LogValue() slog.Value {
	return slog.GroupValue(
		slog.String("type", "reaction"),
		slog.Any("to", m.Recipient),
		slog.String("emoji", m.Emoji),
	)
}

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

// ListReplyMessage is an outbound list-row reply — the reply-class half of
// the FR-131 split (spec 110j). The peer sent an interactive list and we
// echo back the SelectedRowID it offered. Title is optional and carries
// the human-readable label so the audit log + history persistence captures
// what the agent picked (the wire field is `ListResponseMessage.Title`,
// which WhatsApp clients display in the receipt).
//
// ContextStanzaID + ContextSender carry the inbound message-ID and sender
// JID of the *waE2E.ListMessage we are replying to. WhatsApp's wire
// protocol requires the reply to quote the original via ContextInfo —
// without it the server returns "bad stanza" (error 479) and rejects the
// send. This is what reify the FR-131 reply-class semantics: you can only
// echo a row the peer offered first.
type ListReplyMessage struct {
	Recipient       JID
	RowID           string
	Title           string
	ContextStanzaID MessageID
	ContextSender   JID
	// ContextQuotedRaw is the marshalled *waE2E.Message of the inbound
	// ListMessage being replied to. WhatsApp's wire layer requires this
	// echo in ContextInfo.QuotedMessage; without it the server rejects
	// the response with error 479 bad-stanza (#163). The dispatcher
	// hydrates this field from the on-disk raw_proto blob — callers
	// SHOULD NOT populate it directly. Nil is tolerated by Validate()
	// so domain unit tests stay short; the adapter falls back to a
	// nil QuotedMessage which the server then rejects, surfacing the
	// missing-hydration bug instead of hiding it.
	ContextQuotedRaw []byte
}

// isMessage implements the sealed Message interface marker.
func (ListReplyMessage) isMessage() { /* sealed interface marker — intentionally empty */ }

// To returns the recipient JID.
func (m ListReplyMessage) To() JID { return m.Recipient }

// Validate enforces: non-zero recipient, non-empty RowID, non-empty
// ContextStanzaID, non-zero ContextSender. Title is optional.
func (m ListReplyMessage) Validate() error {
	if m.Recipient.IsZero() {
		return fmt.Errorf("%w: ListReplyMessage has zero recipient", ErrInvalidJID)
	}
	if m.RowID == "" {
		return fmt.Errorf("%w: ListReplyMessage has empty rowID", ErrEmptyBody)
	}
	if m.ContextStanzaID == "" {
		return fmt.Errorf("%w: ListReplyMessage has empty contextStanzaId — the WhatsApp wire requires the reply to quote the original list message", ErrEmptyBody)
	}
	if m.ContextSender.IsZero() {
		return fmt.Errorf("%w: ListReplyMessage has zero contextSender — the WhatsApp wire requires the participant JID of the original list message", ErrInvalidJID)
	}
	return nil
}

// LogValue — see TextMessage.LogValue.
func (m ListReplyMessage) LogValue() slog.Value {
	return slog.GroupValue(
		slog.String("type", "list_reply"),
		slog.Any("to", m.Recipient),
		slog.String("rowId", m.RowID),
		slog.String("title", preview(m.Title)),
		slog.String("contextStanzaId", string(m.ContextStanzaID)),
		slog.Any("contextSender", m.ContextSender),
	)
}

// ButtonReplyKind discriminates between buttons-response and template-button-
// reply on the wire. Both share the same domain shape (id + display text),
// only the proto field differs.
type ButtonReplyKind uint8

const (
	// ButtonReplyButtons targets *waE2E.ButtonsResponseMessage. Used when
	// the peer sent a `ButtonsMessage`.
	ButtonReplyButtons ButtonReplyKind = iota
	// ButtonReplyTemplate targets *waE2E.TemplateButtonReplyMessage. Used
	// when the peer sent a `TemplateMessage` with button definitions.
	ButtonReplyTemplate
)

// ButtonReplyMessage is an outbound button-press reply — the reply-class
// half of the FR-131 split (spec 110j). Kind selects which whatsmeow proto
// field gets populated; DisplayText is optional (omitted for templates the
// peer didn't label).
//
// ContextStanzaID + ContextSender carry the inbound message-ID and sender
// JID of the *waE2E.ButtonsMessage / TemplateMessage we are replying to.
// See ListReplyMessage docs for why both fields are required — same wire
// rule (ContextInfo is what makes WhatsApp accept the response as
// reply-class instead of an unsolicited FR-131 violation).
type ButtonReplyMessage struct {
	Recipient       JID
	ButtonID        string
	DisplayText     string
	Kind            ButtonReplyKind
	ContextStanzaID MessageID
	ContextSender   JID
	// ContextQuotedRaw — see ListReplyMessage.ContextQuotedRaw. Same
	// hydration semantics, same wire requirement (#163).
	ContextQuotedRaw []byte
}

// isMessage implements the sealed Message interface marker.
func (ButtonReplyMessage) isMessage() { /* sealed interface marker — intentionally empty */ }

// To returns the recipient JID.
func (m ButtonReplyMessage) To() JID { return m.Recipient }

// Validate enforces: non-zero recipient, non-empty ButtonID, non-empty
// ContextStanzaID, non-zero ContextSender. DisplayText is optional. Kind
// is a uint8 so any value compiles; only the two declared constants are
// honoured by the adapter switch (default branch errors).
func (m ButtonReplyMessage) Validate() error {
	if m.Recipient.IsZero() {
		return fmt.Errorf("%w: ButtonReplyMessage has zero recipient", ErrInvalidJID)
	}
	if m.ButtonID == "" {
		return fmt.Errorf("%w: ButtonReplyMessage has empty buttonID", ErrEmptyBody)
	}
	if m.ContextStanzaID == "" {
		return fmt.Errorf("%w: ButtonReplyMessage has empty contextStanzaId — the WhatsApp wire requires the reply to quote the original buttons message", ErrEmptyBody)
	}
	if m.ContextSender.IsZero() {
		return fmt.Errorf("%w: ButtonReplyMessage has zero contextSender — the WhatsApp wire requires the participant JID of the original buttons message", ErrInvalidJID)
	}
	return nil
}

// LogValue — see TextMessage.LogValue.
func (m ButtonReplyMessage) LogValue() slog.Value {
	kind := "buttons"
	if m.Kind == ButtonReplyTemplate {
		kind = "template_button"
	}
	return slog.GroupValue(
		slog.String("type", "button_reply"),
		slog.String("kind", kind),
		slog.Any("to", m.Recipient),
		slog.String("buttonId", m.ButtonID),
		slog.String("displayText", preview(m.DisplayText)),
		slog.String("contextStanzaId", string(m.ContextStanzaID)),
		slog.Any("contextSender", m.ContextSender),
	)
}
