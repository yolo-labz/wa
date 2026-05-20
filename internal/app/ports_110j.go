package app

import (
	"context"

	"github.com/yolo-labz/wa/v2/internal/domain"
)

// QuotedMessageStore is the secondary port the dispatcher uses to hydrate
// the wire-level *waE2E.Message bytes that an outbound list/button reply
// has to quote via ContextInfo.QuotedMessage (#163).
//
// The bytes returned are the marshalled `*waE2E.Message` protobuf stored
// on `messages.raw_proto` by the sqlitehistory adapter; the whatsmeow
// adapter decodes them and embeds the resulting `*waE2E.Message` into the
// outbound ContextInfo so WhatsApp's wire layer admits the reply
// (without QuotedMessage the server returns error 479 bad-stanza).
//
// Implementations MUST:
//   - Return ErrMessageNotFound when the message ID is unknown so the
//     dispatcher can translate it to ErrInvalidParams (-32602) instead of
//     letting the operator see a generic internal-error after the adapter
//     hop.
//   - Honour ctx cancellation.
//   - Be safe for concurrent reads — the dispatcher may call this from
//     multiple in-flight RPC handlers.
//
// Nil is allowed at the DispatcherConfig boundary. When nil, send.listResponse
// and send.buttonResponse return ErrQuotedMessageStoreNotConfigured
// (-32000 upstream_error) — explicit failure mode beats a silent omission
// that the WhatsApp server would reject anyway.
type QuotedMessageStore interface {
	GetRawProto(ctx context.Context, messageID domain.MessageID) (rawProto []byte, err error)
}

// ErrMessageNotFound is returned by QuotedMessageStore.GetRawProto when
// the message ID is unknown. The dispatcher converts this to
// ErrInvalidParams (-32602) so the operator gets an actionable error
// pointing at their bad contextStanzaId.
var ErrMessageNotFound = newRPCErr(-32117, "message not found")

// ErrQuotedMessageStoreNotConfigured is returned by send.listResponse /
// send.buttonResponse when the dispatcher was constructed without a
// QuotedMessageStore. The wire layer requires ContextInfo.QuotedMessage,
// so without the store there is no way to ship a working reply — fail
// loudly instead of letting the WhatsApp server return error 479.
var ErrQuotedMessageStoreNotConfigured = newRPCErr(-32000, "quoted message store not configured — daemon cannot hydrate reply-class interactive sends; wire a QuotedMessageStore in DispatcherConfig")
