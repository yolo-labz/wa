package domain

import (
	"fmt"
	"log/slog"
	"strings"
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

// Message is the sealed sum type for messages. Only the variants
// declared in this file may satisfy it, because isMessage() is an
// unexported sentinel method.
//
// Variant and Content are part of the interface, not switches in a
// consumer: a projection that type-switches over the variants silently
// mis-labels (or drops) every variant added after it was written.
// Requiring them here makes the omission a compile error instead.
type Message interface {
	isMessage()
	To() JID
	Variant() string
	Content() Content
	Validate() error
}

// Content is the variant-independent payload view of a Message. It is
// what persistence and rendering need from any variant, so neither has
// to keep its own type switch and go stale on the next one.
type Content struct {
	// Text is what a human on the other end actually wrote: a body, a
	// caption, a reaction emoji, the label of a button they tapped.
	// Empty for variants carrying no human text (audio, sticker) and
	// for daemon-generated descriptors like UnknownMessage.Detail,
	// which must never be presented as if a contact had typed it.
	Text string
	// Mime is the attachment's MIME type; empty for non-media variants.
	Mime string
}

// TextMessage is a plain-text outbound message.
type TextMessage struct {
	Recipient   JID
	Body        string
	LinkPreview bool

	// Mentions lists the identities to @mention. When non-empty the
	// whatsmeow adapter sends an ExtendedTextMessage carrying
	// ContextInfo.MentionedJID (a plain Conversation cannot carry
	// ContextInfo) so the recipient's client renders each as a tappable,
	// notifying mention. WhatsApp only renders a mention where the message
	// text contains the matching `@<user-digits>` token; EffectiveBody
	// appends any missing token so the wire and the rendered bubble agree.
	// Every mention MUST be an addressable identity (user/LID/hosted/bot) —
	// groups and channels cannot be mentioned (enforced by Validate).
	Mentions []JID
}

// isMessage implements the sealed Message interface marker.
func (TextMessage) isMessage() { /* sealed interface marker — intentionally empty */ }

// Variant returns the stable wire name of this message variant.
func (TextMessage) Variant() string { return "text" }

// Content returns the body that goes on the wire, mention tokens
// included, so history matches the bubble the recipient sees.
func (m TextMessage) Content() Content { return Content{Text: m.EffectiveBody()} }

// To returns the recipient JID.
func (m TextMessage) To() JID { return m.Recipient }

// Validate enforces: non-zero recipient, non-empty body, body ≤ MaxTextBytes,
// and every mention is a non-zero addressable identity.
func (m TextMessage) Validate() error {
	if m.Recipient.IsZero() {
		return fmt.Errorf("%w: TextMessage has zero recipient", ErrInvalidJID)
	}
	if m.Body == "" {
		return fmt.Errorf("%w: TextMessage has empty body", ErrEmptyBody)
	}
	for _, j := range m.Mentions {
		if j.IsZero() {
			return fmt.Errorf("%w: TextMessage has a zero mention JID", ErrInvalidJID)
		}
		if !j.IsAddressable() {
			return fmt.Errorf("%w: TextMessage mention %q is not addressable (only user/LID/hosted/bot JIDs can be mentioned)", ErrInvalidJID, j.String())
		}
	}
	// The wire carries EffectiveBody (Body plus any appended @<number> tokens),
	// so the size limit is enforced against the effective payload, not the raw
	// Body. Mentions are validated first so EffectiveBody only runs over
	// addressable JIDs.
	if eff := m.EffectiveBody(); len(eff) > MaxTextBytes {
		return fmt.Errorf("%w: TextMessage body %d > %d bytes", ErrMessageTooLarge, len(eff), MaxTextBytes)
	}
	return nil
}

// EffectiveBody returns Body with an `@<user-digits>` token appended for
// every mention whose token is not already present. WhatsApp renders a
// tappable mention only where the text carries the `@<number>` token that
// matches a ContextInfo.MentionedJID entry, so a caller that supplies
// mentions but omits the token would otherwise get a silent, non-rendering
// send. When the caller already wrote the token (e.g. `--body "oi @5581…"`)
// this is a no-op. Callers with no mentions get Body unchanged, byte-for-byte.
func (m TextMessage) EffectiveBody() string {
	body := m.Body
	for _, j := range m.Mentions {
		if j.IsZero() {
			continue
		}
		tok := "@" + j.User()
		if bodyHasMentionToken(body, tok) {
			continue
		}
		if body != "" {
			body += " "
		}
		body += tok
	}
	return body
}

// bodyHasMentionToken reports whether tok ("@<digits>") appears in body as a
// standalone mention token — the `@` at a word start AND the digits ending on
// a non-digit boundary. WhatsApp only renders `@<number>` as a mention at a
// word start, so `joao@123` does NOT count (else the real token is dropped and
// the mention silently fails to render); and `@5581` is NOT counted inside the
// longer number `@55819999` (else the shorter mention's token is dropped).
func bodyHasMentionToken(body, tok string) bool {
	from := 0
	for {
		i := strings.Index(body[from:], tok)
		if i < 0 {
			return false
		}
		start := from + i
		end := start + len(tok)
		// Left: word start — string start or a non-alphanumeric byte before `@`.
		// ponytail: ASCII boundary only; a multibyte letter glued directly
		// before `@` (rare) is treated as a boundary and the token counts.
		leftOK := start == 0 || !isASCIIAlnum(body[start-1])
		// Right: not a prefix of a longer number.
		rightOK := end == len(body) || body[end] < '0' || body[end] > '9'
		if leftOK && rightOK {
			return true
		}
		from = start + 1
	}
}

// isASCIIAlnum reports whether b is an ASCII letter or digit.
func isASCIIAlnum(b byte) bool {
	return (b >= '0' && b <= '9') || (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z')
}

// logValue builds the log group every message variant shares — the wire
// variant name and the recipient — followed by whatever that variant
// adds. Routing all twelve through Variant() is why the "type" key in
// the logs and the "kind" field on the subscriber event stream cannot
// drift apart.
func logValue(m Message, extra ...slog.Attr) slog.Value {
	attrs := append(
		make([]slog.Attr, 0, len(extra)+2),
		slog.String("type", m.Variant()),
		slog.Any("to", m.To()),
	)
	return slog.GroupValue(append(attrs, extra...)...)
}

// LogValue implements slog.LogValuer so a TextMessage logs with its
// recipient + size + truncated body preview rather than the full raw
// body (which can be 64 KiB). Spec 016 FR-013 / T068.
func (m TextMessage) LogValue() slog.Value {
	return logValue(
		m,
		slog.Int("bytes", len(m.Body)),
		slog.Int("mentions", len(m.Mentions)),
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

	// Filename is the optional display filename for document sends (the
	// FileName/Title fields on the wire). Bytes/SHA256 sources carry no
	// daemon-visible path, so without it the recipient's client renders the
	// document as an unopenable ".bin" attachment. The adapter falls back to
	// the Path basename, then to a name generated from the resolved MIME.
	Filename string

	// Bytes is the inline plaintext payload (mutually exclusive with Path
	// and SHA256). When set, len(Bytes) MUST be ≤ MaxMediaBytes.
	Bytes []byte
	// SHA256 is the lowercase-hex sha256 handle of an object already in the
	// media store (mutually exclusive with Path and Bytes).
	SHA256 string

	// PTT marks an audio payload as a push-to-talk voice note rather than an
	// attached audio file. It is the single field that decides which of the
	// two the recipient's client renders: a waveform bubble that plays inline
	// and reports "played" receipts, or a file row. Nothing else about the
	// upload differs, which is why this rides on MediaMessage instead of a
	// second send path — the payload-source seam, MIME resolution, remote
	// upload and idempotency wrapper are all identical.
	//
	// wa does not transcode. WhatsApp clients render the waveform only for
	// Opus-in-Ogg, so a caller setting PTT should be uploading
	// "audio/ogg; codecs=opus"; anything else arrives as a voice note the
	// recipient cannot play.
	PTT bool

	// Seconds is the audio duration the recipient's client shows before
	// downloading the payload. Optional — zero omits the hint and the client
	// discovers the duration itself on play — but a voice note with no
	// duration renders as a zero-length bubble in most clients, so a caller
	// that knows the length should send it. wa has no duration probe; the
	// number has to come from whoever produced the audio.
	Seconds int
}

// MaxAudioSeconds bounds MediaMessage.Seconds. A single upload is capped at
// MaxMediaBytes (16 MiB), which even at a wasteful bitrate cannot hold a day
// of audio, so a larger value is a caller bug rather than a real duration.
//
// It is exported because the wire field is a uint32: the adapter re-states the
// bound at the conversion so the narrowing is provably in range at that line,
// rather than only in this file. Two checks of one named constant, not two
// numbers that can drift.
const MaxAudioSeconds = 24 * 60 * 60

// isMessage implements the sealed Message interface marker.
func (MediaMessage) isMessage() { /* sealed interface marker — intentionally empty */ }

// Variant returns the stable wire name of this message variant.
func (MediaMessage) Variant() string { return "media" }

// Content returns the photo's caption, plus its MIME.
func (m MediaMessage) Content() Content { return Content{Text: m.Caption, Mime: m.Mime} }

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
	return m.AudioValidate()
}

// AudioValidate enforces that the two audio-shape hints — PTT and Seconds —
// are only set on an audio payload, and that a duration is a plausible one.
//
// It is split out for the same reason SourceValidate is: the app layer runs
// it against caller-supplied params, before a recipient exists, so a `ptt`
// on an image comes back as -32602 invalid_params at the param boundary
// instead of an opaque internal error from deep inside the adapter. Validate
// calls it too, so the rule has one home and cannot drift between the two.
//
// The gate is the declared MIME, not the resolved one. Resolution (extension,
// then store sniff, then magic bytes) happens in the adapter after upload
// selection, and it is deliberately not consulted here: a caller asking for a
// voice note has to name the audio type, because the type also decides whether
// the recipient can play what arrives.
func (m MediaMessage) AudioValidate() error {
	if !m.PTT && m.Seconds == 0 {
		return nil
	}
	if !strings.HasPrefix(m.Mime, "audio/") {
		return fmt.Errorf("%w: MediaMessage sets ptt/seconds on mime %q (audio/* required)", ErrEmptyBody, m.Mime)
	}
	if m.Seconds < 0 || m.Seconds > MaxAudioSeconds {
		return fmt.Errorf("%w: MediaMessage seconds %d outside [0,%d]", ErrEmptyBody, m.Seconds, MaxAudioSeconds)
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
	return logValue(
		m,
		slog.String("mime", m.Mime),
		slog.String("source", source),
		slog.Int("bytes", len(m.Bytes)),
		slog.Bool("ptt", m.PTT),
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

// Variant returns the stable wire name of this message variant.
func (AudioMessage) Variant() string { return "audio" }

// Content returns the audio MIME; a voice note carries no text a
// human typed.
func (m AudioMessage) Content() Content { return Content{Mime: m.Mime} }

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
	return logValue(
		m,
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

// Variant returns the stable wire name of this message variant.
func (VideoMessage) Variant() string { return "video" }

// Content returns the clip's caption, plus its MIME.
func (m VideoMessage) Content() Content { return Content{Text: m.Caption, Mime: m.Mime} }

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
	return logValue(
		m,
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

// Variant returns the stable wire name of this message variant.
func (DocumentMessage) Variant() string { return "document" }

// Content returns the attachment's caption, plus its MIME.
// Filename stays out: it is filesystem metadata, not prose.
func (m DocumentMessage) Content() Content { return Content{Text: m.Caption, Mime: m.Mime} }

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
	return logValue(
		m,
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

// Variant returns the stable wire name of this message variant.
func (StickerMessage) Variant() string { return "sticker" }

// Content returns the sticker MIME; a sticker carries no text.
func (m StickerMessage) Content() Content { return Content{Mime: m.Mime} }

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
	return logValue(
		m,
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

// Variant returns the stable wire name of this message variant.
func (ContactCard) Variant() string { return "contact" }

// Content returns the shared contact's display name. The vCard
// stays out: it is a structured record, not a line of text.
func (m ContactCard) Content() Content { return Content{Text: m.DisplayName} }

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
	return logValue(
		m,
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

// Variant returns the stable wire name of this message variant.
func (LocationPin) Variant() string { return "location" }

// Content returns the pin's place name. Address stays out: it is
// the same place said twice, and Text is a single line.
func (m LocationPin) Content() Content { return Content{Text: m.Name} }

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
	return logValue(
		m,
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

// Variant returns the stable wire name of this message variant.
func (UnknownMessage) Variant() string { return "unknown" }

// Content is empty. Detail is a daemon-generated descriptor of a
// variant we do not model — it is not something a contact wrote.
func (UnknownMessage) Content() Content { return Content{} }

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
	return logValue(
		m,
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

// Variant returns the stable wire name of this message variant.
func (ReactionMessage) Variant() string { return "reaction" }

// Content returns the emoji. What it decorates is addressed by
// TargetID, not by anything in the text.
func (m ReactionMessage) Content() Content { return Content{Text: m.Emoji} }

// To returns the recipient JID (the chat the target message lives in).
func (m ReactionMessage) To() JID { return m.Recipient }

// LogValue — see TextMessage.LogValue.
func (m ReactionMessage) LogValue() slog.Value {
	return logValue(
		m,
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

// Variant returns the stable wire name of this message variant.
func (ListReplyMessage) Variant() string { return "list_reply" }

// Content returns the label of the row the contact picked. RowID
// stays out: it is our own menu key, not their words.
func (m ListReplyMessage) Content() Content { return Content{Text: m.Title} }

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
	return logValue(
		m,
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

// Variant returns the stable wire name of this message variant.
func (ButtonReplyMessage) Variant() string { return "button_reply" }

// Content returns the label of the button the contact tapped.
// ButtonID stays out: it is our own key, not their words.
func (m ButtonReplyMessage) Content() Content { return Content{Text: m.DisplayText} }

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
	return logValue(
		m,
		slog.String("kind", kind),
		slog.String("buttonId", m.ButtonID),
		slog.String("displayText", preview(m.DisplayText)),
		slog.String("contextStanzaId", string(m.ContextStanzaID)),
		slog.Any("contextSender", m.ContextSender),
	)
}
