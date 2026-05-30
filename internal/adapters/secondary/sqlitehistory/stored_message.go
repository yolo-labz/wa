package sqlitehistory

// StoredMessage is the persistence DTO for a single message row in
// messages.db. It carries all metadata needed for INSERT and SELECT
// operations. This type lives in the adapter layer (sqlitehistory),
// not in internal/domain, because it carries storage-specific fields
// (is_from_me, raw_proto) that are not part of the domain model.
//
// Feature 009 — spec FR-022, data-model.md §StoredMessage.
type StoredMessage struct {
	ChatJID   string // e.g., "group@g.us" or "user@s.whatsapp.net"
	SenderJID string // e.g., "alice@s.whatsapp.net" (actual sender)
	MessageID string // WhatsApp message ID (e.g., "3EB0ABC123") — dedup key
	Timestamp int64  // Unix seconds (WhatsApp server timestamp)
	Body      string // Text body or media caption
	MediaType string // MIME type (e.g., "image/jpeg") or "" for text
	Caption   string // Media caption (separate from body for media messages)
	IsFromMe  bool   // true for outbound messages
	RawProto  []byte // Optional: raw protobuf bytes for lossless storage
	PushName  string // Sender's display name at time of message

	// SenderAltJID is the alternative-namespace JID for SenderJID. When
	// SenderJID is a phone-number JID (`@s.whatsapp.net`), SenderAltJID
	// is the LID (`@lid`); when SenderJID is a LID, SenderAltJID is the
	// PN. Empty string when whatsmeow has not yet surfaced the mapping
	// (most legacy rows + the history-sync path land here). Spec 107.
	SenderAltJID string
	// AddressingMode is "pn" or "lid" — which namespace the sender was
	// addressed by on the wire. Empty string on legacy rows. Spec 107.
	AddressingMode string

	// InteractiveJSON is the JSON-encoded domain.InteractivePayload for a
	// list/button/native-flow reply (issue #201, FR-130), or nil/empty for
	// ordinary messages. It is persisted verbatim to the messages
	// `interactive_json` BLOB column so the client read path can recover
	// which menu option the user selected. The encoding is the wire shape
	// {"subtype","prompt","options":[{"id","label"}]}; build it with
	// MarshalInteractive and read it back with UnmarshalInteractive.
	InteractiveJSON []byte
}
