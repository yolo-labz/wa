package whatsmeow

import (
	"fmt"
	"strconv"
	"time"

	waE2E "go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"

	"github.com/yolo-labz/wa/v2/internal/domain"
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

	if evt, detail, ok := translateConnectivityHealth(id, nowFn, rawEvt); ok {
		return evt, sideEffectNone, detail
	}

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
		// whatsmeow emits *events.HistorySync after downloading and
		// decoding the blob (ManualHistorySyncDownload=false). The Data
		// field is populated with the decoded HistorySync protobuf.
		// Route to the background worker for batch-insert.
		return nil, sideEffectHistorySync, ""

	default:
		// Unknown event type. Per Clarifications round 2 Q2, the caller
		// records an AuditPanic so a Renovate whatsmeow bump that
		// introduces a new event type is visible on first occurrence
		// instead of being silently dropped.
		return nil, sideEffectUnknown, fmt.Sprintf("unknown whatsmeow event type: %T", rawEvt)
	}
}

// translateConnectivityHealth handles every whatsmeow event that maps
// onto domain.ConnectivityHealthEvent. Extracted from translateEvent's
// big switch so that file's gocyclo stays under the linter ceiling and
// new health states (e.g. 110g soft-stale) can land here without
// reopening the dispatch trunk.
func translateConnectivityHealth(id domain.EventID, nowFn func() time.Time, rawEvt any) (domain.Event, string, bool) {
	switch evt := rawEvt.(type) {
	case *events.KeepAliveTimeout:
		detail := fmt.Sprintf("errorCount=%d lastSuccess=%s",
			evt.ErrorCount, evt.LastSuccess.UTC().Format(time.RFC3339))
		return domain.ConnectivityHealthEvent{
			ID:     id,
			TS:     nowFn(),
			State:  domain.HealthKeepAliveTimeout,
			Detail: detail,
		}, detail, true

	case *events.KeepAliveRestored:
		return domain.ConnectivityHealthEvent{
			ID:    id,
			TS:    nowFn(),
			State: domain.HealthKeepAliveRestored,
		}, "", true

	case *events.StreamReplaced:
		return domain.ConnectivityHealthEvent{
			ID:    id,
			TS:    nowFn(),
			State: domain.HealthStreamReplaced,
		}, "", true

	case *events.ConnectFailure:
		detail := fmt.Sprintf("reason=%d %s", int(evt.Reason), evt.Reason.String())
		if evt.Message != "" {
			detail += " message=" + evt.Message
		}
		return domain.ConnectivityHealthEvent{
			ID:     id,
			TS:     nowFn(),
			State:  domain.HealthConnectFailure,
			Detail: detail,
		}, detail, true

	case *events.TemporaryBan:
		detail := fmt.Sprintf("code=%d expireSec=%d", int(evt.Code), int(evt.Expire.Seconds()))
		return domain.ConnectivityHealthEvent{
			ID:     id,
			TS:     nowFn(),
			State:  domain.HealthTemporaryBan,
			Detail: detail,
		}, detail, true

	case *events.ClientOutdated:
		return domain.ConnectivityHealthEvent{
			ID:    id,
			TS:    nowFn(),
			State: domain.HealthClientOutdated,
		}, "", true

	case *events.ManualLoginReconnect:
		return domain.ConnectivityHealthEvent{
			ID:    id,
			TS:    nowFn(),
			State: domain.HealthManualLoginReconnect,
		}, "", true

	default:
		return nil, "", false
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
		ID:          id,
		TS:          evt.Info.Timestamp,
		From:        from,
		PushName:    evt.Info.PushName,
		Message:     extractMessageBody(evt),
		Interactive: extractInteractive(evt.Message),
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
	if aud := evt.Message.GetAudioMessage(); aud != nil {
		return domain.AudioMessage{
			Recipient: chat,
			Path:      aud.GetDirectPath(),
			Mime:      aud.GetMimetype(),
			Seconds:   int(aud.GetSeconds()),
			PTT:       aud.GetPTT(),
		}
	}
	if vid := evt.Message.GetVideoMessage(); vid != nil {
		return domain.VideoMessage{
			Recipient: chat,
			Path:      vid.GetDirectPath(),
			Mime:      vid.GetMimetype(),
			Caption:   vid.GetCaption(),
			Seconds:   int(vid.GetSeconds()),
			IsGif:     vid.GetGifPlayback(),
		}
	}
	if doc := evt.Message.GetDocumentMessage(); doc != nil {
		return domain.DocumentMessage{
			Recipient: chat,
			Path:      doc.GetDirectPath(),
			Mime:      doc.GetMimetype(),
			Filename:  doc.GetFileName(),
			Caption:   doc.GetCaption(),
		}
	}
	if stk := evt.Message.GetStickerMessage(); stk != nil {
		return domain.StickerMessage{
			Recipient:  chat,
			Path:       stk.GetDirectPath(),
			Mime:       stk.GetMimetype(),
			IsAnimated: stk.GetIsAnimated(),
		}
	}
	if ctc := evt.Message.GetContactMessage(); ctc != nil {
		return domain.ContactCard{
			Recipient:   chat,
			DisplayName: ctc.GetDisplayName(),
			VCard:       ctc.GetVcard(),
		}
	}
	if loc := evt.Message.GetLocationMessage(); loc != nil {
		return domain.LocationPin{
			Recipient: chat,
			Latitude:  loc.GetDegreesLatitude(),
			Longitude: loc.GetDegreesLongitude(),
			Name:      loc.GetName(),
			Address:   loc.GetAddress(),
		}
	}
	// Unrecognised oneof branch — surface an UnknownMessage so the event
	// is VISIBLE to downstream consumers and audit, not silently flattened
	// to an empty TextMessage. Feature 018 T1-12 / R-05 (CLAUDE.md rule 12:
	// no silent fallbacks).
	return domain.UnknownMessage{
		Recipient: chat,
		Detail:    unknownMessageDetail(evt.Message),
	}
}

// unknownMessageDetail returns a short descriptor identifying which
// top-level waE2E.Message oneof branch is populated, by probing the
// handful of non-mapped branches we do not yet translate. The name is
// intentionally truthful: if this returns "unknown", the event is a
// genuinely new variant that the translator has never seen.
func unknownMessageDetail(m *waE2E.Message) string {
	switch {
	case m == nil:
		return "nil"
	case m.GetProtocolMessage() != nil:
		return "protocolMessage"
	case m.GetSenderKeyDistributionMessage() != nil:
		return "senderKeyDistributionMessage"
	case m.GetButtonsMessage() != nil:
		return "buttonsMessage"
	case m.GetTemplateMessage() != nil:
		return "templateMessage"
	case m.GetInteractiveMessage() != nil:
		return "interactiveMessage"
	case m.GetListMessage() != nil:
		return "listMessage"
	case m.GetPollCreationMessage() != nil:
		return "pollCreationMessage"
	case m.GetPollUpdateMessage() != nil:
		return "pollUpdateMessage"
	case m.GetEditedMessage() != nil:
		return "editedMessage"
	case m.GetEphemeralMessage() != nil:
		return "ephemeralMessage"
	case m.GetViewOnceMessage() != nil, m.GetViewOnceMessageV2() != nil, m.GetViewOnceMessageV2Extension() != nil:
		return "viewOnceMessage"
	default:
		return "unknown"
	}
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
