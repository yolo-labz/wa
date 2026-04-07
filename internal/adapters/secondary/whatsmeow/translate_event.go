package whatsmeow

import (
	"fmt"
	"strconv"
	"time"

	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"

	"github.com/yolo-labz/wa/internal/domain"
)

// eventSideEffect communicates to the caller of translateEvent how to
// dispatch side-effecting work the translator itself does not perform.
// Splitting pure translation from side effects keeps this file unit-testable
// without mocking the Adapter.
type eventSideEffect int

const (
	// sideEffectNone means the returned domain.Event is non-nil and should
	// be enqueued onto the bounded eventCh.
	sideEffectNone eventSideEffect = iota
	// sideEffectIgnore means the whatsmeow event is known but has no
	// corresponding domain event (e.g. *events.QR, consumed via GetQRChannel).
	sideEffectIgnore
	// sideEffectLoggedOut means the caller must clear the session and emit
	// a PairingEvent{State: PairFailure}.
	sideEffectLoggedOut
	// sideEffectHistorySync means the caller must route the raw event into
	// handleHistorySync (download + persist + on-demand routing).
	sideEffectHistorySync
	// sideEffectUnknown means the caller must record an AuditPanic entry
	// with the accompanying detail string, so a Renovate whatsmeow bump
	// that introduces a new event type surfaces on first occurrence per
	// Clarifications session 2026-04-07 round 2.
	sideEffectUnknown
)

// translateEvent is the pure event-translation function consumed by the
// Adapter's handleWAEvent method (defined in commit 4). It takes a
// monotonic sequence number, a "now" function (so tests can inject a
// deterministic clock), and a raw whatsmeow event, and returns the
// translated domain.Event (or nil), a side-effect hint, and an optional
// detail string for audit logging.
//
// This function never blocks, never performs I/O, and never touches the
// Adapter's mutable state. All side effects are delegated to the caller
// via the sideEffect return.
func translateEvent(seq uint64, nowFn func() time.Time, rawEvt any) (domain.Event, eventSideEffect, string) {
	id := domain.EventID(strconv.FormatUint(seq, 10))

	switch evt := rawEvt.(type) {
	case *events.Message:
		return translateMessage(id, evt), sideEffectNone, ""

	case *events.Receipt:
		return translateReceipt(id, evt), sideEffectNone, ""

	case *events.Connected:
		return domain.ConnectionEvent{
			ID:    id,
			TS:    nowFn(),
			State: domain.ConnConnected,
		}, sideEffectNone, ""

	case *events.Disconnected:
		return domain.ConnectionEvent{
			ID:    id,
			TS:    nowFn(),
			State: domain.ConnDisconnected,
		}, sideEffectNone, ""

	case *events.LoggedOut:
		return nil, sideEffectLoggedOut, fmt.Sprintf("logged out: onConnect=%t reason=%s", evt.OnConnect, evt.Reason.String())

	case *events.PairSuccess:
		return domain.PairingEvent{
			ID:    id,
			TS:    nowFn(),
			State: domain.PairSuccess,
		}, sideEffectNone, ""

	case *events.PairError:
		detail := ""
		if evt.Error != nil {
			detail = evt.Error.Error()
		}
		return domain.PairingEvent{
			ID:    id,
			TS:    nowFn(),
			State: domain.PairFailure,
		}, sideEffectNone, detail

	case *events.QR:
		// QR is delivered via GetQRChannel in pair.go, not via the event
		// handler. Drop silently here; the pair-flow goroutine owns it.
		return nil, sideEffectIgnore, ""

	case *events.HistorySync:
		// whatsmeow emits *events.HistorySync (not HistorySyncNotification
		// — that's the undecoded protobuf consumed internally). Under
		// ManualHistorySyncDownload=true the Data field is already
		// populated and the caller must persist it via commit 4's
		// handleHistorySync. Route with sideEffectHistorySync.
		return nil, sideEffectHistorySync, ""

	default:
		// Unknown event type. Per Clarifications round 2 Q2, the caller
		// records an AuditPanic so a Renovate whatsmeow bump that
		// introduces a new event type is visible on first occurrence
		// instead of being silently dropped.
		return nil, sideEffectUnknown, fmt.Sprintf("unknown whatsmeow event type: %T", rawEvt)
	}
}

// --- Per-event translators ---

func translateMessage(id domain.EventID, evt *events.Message) domain.Event {
	from, err := toDomain(evt.Info.Sender)
	if err != nil {
		// Sender JID was malformed. Yield a best-effort event with a
		// zero From. Commit 4's audit layer will surface the parse
		// failure via the recordAudit call path.
		from = domain.JID{}
	}
	return domain.MessageEvent{
		ID:       id,
		TS:       evt.Info.Timestamp,
		From:     from,
		PushName: evt.Info.PushName,
		Message:  extractMessageBody(evt),
	}
}

// extractMessageBody maps a whatsmeow *waE2E.Message into one of the three
// domain.Message variants. Only the simplest shapes are handled in commit 3;
// richer variants (quoted replies, view-once, buttons) can be added in
// later commits without changing this function's signature.
func extractMessageBody(evt *events.Message) domain.Message {
	chat, err := toDomain(evt.Info.Chat)
	if err != nil {
		chat = domain.JID{}
	}
	if evt.Message == nil {
		return domain.TextMessage{Recipient: chat, Body: ""}
	}
	if body := evt.Message.GetConversation(); body != "" {
		return domain.TextMessage{Recipient: chat, Body: body}
	}
	if ext := evt.Message.GetExtendedTextMessage(); ext != nil && ext.GetText() != "" {
		return domain.TextMessage{Recipient: chat, Body: ext.GetText()}
	}
	if react := evt.Message.GetReactionMessage(); react != nil {
		return domain.ReactionMessage{
			Recipient: chat,
			TargetID:  domain.MessageID(react.GetKey().GetID()),
			Emoji:     react.GetText(),
		}
	}
	if img := evt.Message.GetImageMessage(); img != nil {
		return domain.MediaMessage{
			Recipient: chat,
			Path:      img.GetDirectPath(),
			Mime:      img.GetMimetype(),
			Caption:   img.GetCaption(),
		}
	}
	// Unknown message shape — yield an empty TextMessage placeholder.
	// Commit 4 will record a non-fatal audit entry when this happens.
	return domain.TextMessage{Recipient: chat, Body: ""}
}

func translateReceipt(id domain.EventID, evt *events.Receipt) domain.Event {
	chat, err := toDomain(evt.Chat)
	if err != nil {
		chat = domain.JID{}
	}
	var msgID domain.MessageID
	if len(evt.MessageIDs) > 0 {
		msgID = domain.MessageID(evt.MessageIDs[0])
	}
	return domain.ReceiptEvent{
		ID:        id,
		TS:        evt.Timestamp,
		Chat:      chat,
		MessageID: msgID,
		Status:    mapReceiptType(evt.Type),
	}
}

// mapReceiptType translates whatsmeow's ReceiptType (a string enum) into
// the domain's ReceiptStatus. The default clause is exhaustive enough
// that a new whatsmeow value falls back to Delivered rather than zero.
func mapReceiptType(t types.ReceiptType) domain.ReceiptStatus {
	switch t {
	case types.ReceiptTypeDelivered, types.ReceiptTypeSender:
		return domain.ReceiptDelivered
	case types.ReceiptTypeRead, types.ReceiptTypeReadSelf:
		return domain.ReceiptRead
	case types.ReceiptTypePlayed:
		return domain.ReceiptPlayed
	default:
		return domain.ReceiptDelivered
	}
}
