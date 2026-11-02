package domain

// HistoryRecord is one message row exactly as the history store persists
// it: the WhatsApp identity fields, the sanitized body, the media hints
// needed to reconstruct a download, and the optional interactive-reply
// payload.
//
// It lives in the domain core rather than in either adapter because the
// whatsmeow adapter produces these records and the sqlitehistory adapter
// stores them, and those two must not import each other — the dependency
// rule points both at the domain. Sharing the record here keeps the
// InsertRaw signature readable instead of a 13-positional-parameter list
// (go:S107). The alternative considered earlier — a shared "types"
// package — is exactly what the domain already is.
type HistoryRecord struct {
	ChatJID         string
	SenderJID       string
	MessageID       string
	Timestamp       int64
	Body            string
	MediaType       string
	Caption         string
	PushName        string
	IsFromMe        bool
	RawProto        []byte
	SenderAltJID    string
	AddressingMode  string
	InteractiveJSON []byte
}
