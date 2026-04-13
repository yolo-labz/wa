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
}
