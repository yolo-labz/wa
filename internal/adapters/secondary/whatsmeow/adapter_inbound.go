// adapter_inbound.go is the inbound half of the Adapter: the whatsmeow
// event-handler entry point and the persistence hooks it drives. Split
// from adapter.go (ARCH-04) along the same file-per-responsibility seam
// as send.go / pair.go / history.go — see the C-002 architecture note on
// the Adapter struct.
package whatsmeow

import (
	"context"
	"strconv"

	"go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/types/events"
	"google.golang.org/protobuf/proto"

	"github.com/yolo-labz/wa/v2/internal/domain"
)

// handleWAEvent is the whatsmeow event dispatcher. It translates raw
// events via the pure translateEvent helper from commit 3 and enqueues
// the resulting domain.Event onto eventCh. It is invoked synchronously
// by whatsmeow (SynchronousAck=true) so the return value tells whatsmeow
// whether to ack the upstream message.
//
// Return semantics:
//   - true: the event was successfully handled (queued, ignored, or
//     routed to a side-effect handler). whatsmeow will ack upstream.
//   - false: the event could not be queued (buffer full, clientCtx
//     cancelled). whatsmeow will NOT ack; upstream will redeliver.
func (a *Adapter) handleWAEvent(rawEvt any) bool {
	// Spec 110g diagnostics: log the offline-message count delivered on
	// every (re)connect — the decisive signal for the soft-stale recover
	// path. count>0 ⇒ the recover reconnect FLUSHED queued inbound (the
	// link was a real zombie dropping messages, so auto-recover is
	// load-bearing); count==0 ⇒ the account was merely quiet for the
	// threshold window and the reconnect was a harmless no-op. Observability
	// only; OfflineSyncCompleted is not a domain event (translateEvent
	// ignores it).
	if osc, ok := rawEvt.(*events.OfflineSyncCompleted); ok {
		a.logger.Info("offline sync completed",
			"count", osc.Count, "profile", a.profile)
	}
	seq := a.eventSeq.Add(1)
	translated, effect, detail := translateEvent(seq, a.nowFn, rawEvt)

	switch effect {
	case sideEffectIgnore:
		return true
	case sideEffectLoggedOut:
		// R-07: full wipe on events.LoggedOut. Dispatch to goroutine so
		// the event handler returns promptly (SynchronousAck=true); the
		// wipe closes the underlying SQLite handles and must not run
		// on the same stack as the handler. panicWg lets Close() wait
		// for this goroutine before its own container close — PR #136.
		a.panicWg.Add(1)
		go func() {
			defer a.panicWg.Done()
			_ = a.Panic(context.Background(), "logged_out:"+detail)
		}()
		evt := domain.PairingEvent{
			ID:    domain.EventID(strconv.FormatUint(seq, 10)),
			TS:    a.nowFn(),
			State: domain.PairFailure,
		}
		return a.enqueueBounded(seq, evt)
	case sideEffectHistorySync:
		// Feature 009: dispatch to background goroutine for download,
		// decode, and batch-insert. Non-blocking — FR-009.
		a.dispatchHistorySync(rawEvt)
		return true
	case sideEffectUnknown:
		a.recordAuditDetail(domain.AuditPanic, domain.JID{}, "unknown_event", detail)
		return true
	case sideEffectNone:
		if translated == nil {
			return true
		}
		// Spec 110g: track websocket state for the soft-stale watchdog's
		// WebsocketProbe port. Adapter is the only layer that sees the
		// raw events.Connected / events.Disconnected, so the translation
		// boundary is where the atomic flips.
		if ce, ok := translated.(domain.ConnectionEvent); ok {
			a.wsConnected.Store(ce.State == domain.ConnConnected)
		}
		// Persist delivery/read receipts so a caller can confirm a send
		// landed (not just server-accepted) via `wa thread`. Best-effort:
		// a persistence failure must not block event delivery (FR-001).
		if re, ok := translated.(domain.ReceiptEvent); ok {
			a.persistReceipt(re)
		}
		// Signal a waiting Pair() caller on PairSuccess. Non-blocking
		// send into the buffered channel — drop if a previous unread
		// signal is still queued (only one pairing in flight at a time).
		if pe, ok := translated.(domain.PairingEvent); ok && pe.State == domain.PairSuccess {
			select {
			case a.pairSuccessCh <- struct{}{}:
			default:
			}
		}
		// Feature 009 — FR-001: persist inbound MessageEvents to messages.db.
		// Non-blocking: persistence failure MUST NOT prevent event delivery.
		if _, ok := translated.(domain.MessageEvent); ok {
			a.persistInboundMessage(rawEvt)
		}
		if !a.enqueueBounded(seq, translated) {
			return false
		}
		return true
	default:
		return true
	}
}

// persistInboundMessage extracts metadata from the raw whatsmeow event
// and persists it to messages.db via historyContainer.InsertRaw. Called
// from the sideEffectNone branch of handleWAEvent for MessageEvents
// only. Non-blocking: failure is logged but does not prevent event
// delivery. Feature 009 — FR-001.
func (a *Adapter) persistInboundMessage(rawEvt any) {
	if a.history == nil {
		return
	}
	wmEvt, ok := rawEvt.(*events.Message)
	if !ok || wmEvt == nil {
		return
	}

	chatJID := wmEvt.Info.Chat.String()
	senderJID := wmEvt.Info.Sender.String()
	messageID := wmEvt.Info.ID
	ts := wmEvt.Info.Timestamp.Unix()
	pushName := wmEvt.Info.PushName
	isFromMe := wmEvt.Info.IsFromMe

	// Spec 107: capture the addressing-mode duality whatsmeow surfaces
	// on every inbound message. Empty when whatsmeow has not yet
	// learned the mapping; the v5 schema columns are nullable so empty
	// strings hit the database as NULL via the InsertRaw boundary.
	var senderAltJID string
	if !wmEvt.Info.SenderAlt.IsEmpty() {
		senderAltJID = wmEvt.Info.SenderAlt.String()
	}
	addressingMode := string(wmEvt.Info.AddressingMode)

	body, mediaType, caption := extractBodyAndMedia(wmEvt)

	// Marshal the full inner message so media-download can reconstruct the
	// encrypted media hints later (FR-050). proto.Marshal on a nil message
	// is a programming error at this point — wmEvt.Message was already
	// inspected by extractBodyAndMedia.
	var rawProto []byte
	if wmEvt.Message != nil {
		b, marshalErr := proto.Marshal(wmEvt.Message)
		if marshalErr != nil {
			a.recordAuditDetail(domain.AuditPanic, domain.JID{}, "persist_msg_marshal", marshalErr.Error())
			// Fall through with nil rawProto — row still persists, just
			// media.download will fail with not_cached until a re-sync
			// over HS2 fills in raw_proto.
		} else {
			rawProto = b
		}
	}

	// Issue #201: persist the interactive reply selection (list row /
	// button / native-flow pick) so the client read path can surface which
	// menu option the user chose. nil for ordinary messages → SQL NULL.
	interactiveJSON := a.interactiveJSONForPersist(wmEvt.Message)

	if err := a.history.InsertRawInteractive(
		context.Background(),
		chatJID, senderJID, messageID, ts,
		body, mediaType, caption, pushName, isFromMe, rawProto,
		senderAltJID, addressingMode, interactiveJSON,
	); err != nil {
		a.recordAuditDetail(domain.AuditPanic, domain.JID{}, "persist_msg", err.Error())
	}

	// FR-028: refresh the local contact mirror with the pushName the
	// server just advertised. Only for inbound (not self-authored) user
	// JIDs — group JIDs carry the group as Chat and the sender as Sender,
	// so we key on wmEvt.Info.Sender which is always a user JID.
	if a.pushNameSink != nil && !isFromMe && pushName != "" {
		senderDomainJID, jidErr := toDomain(wmEvt.Info.Sender)
		if jidErr == nil && !senderDomainJID.IsZero() && !senderDomainJID.IsGroup() {
			a.pushNameSink(context.Background(), senderDomainJID, pushName)
		}
	}
}

// interactiveJSONForPersist returns the JSON-encoded interactive reply
// selection for msg, or nil when msg carries no interactive reply (issue
// #201). A marshal failure is audited and treated as "no selection" — the
// row still persists with a NULL interactive_json because the body +
// raw_proto are the load-bearing fields and the selection is best-effort.
// Extracted from persistInboundMessage to keep that function below the
// gocyclo ceiling.
func (a *Adapter) interactiveJSONForPersist(msg *waE2E.Message) []byte {
	payload := extractInteractive(msg)
	if payload == nil {
		return nil
	}
	b, err := marshalInteractiveJSON(payload)
	if err != nil {
		a.recordAuditDetail(domain.AuditPanic, domain.JID{}, "persist_msg_interactive", err.Error())
		return nil
	}
	return b
}

// SetPushNameRefresher wires an app-layer policy object that receives
// (jid, pushName) for every inbound *events.Message with a non-empty
// pushName. The adapter itself makes no decision about whether to upsert
// — that policy lives in the app layer (FR-028). Calling with nil
// disables the hook. Safe to call once at boot; not safe for concurrent
// updates (composition root calls it exactly once).
func (a *Adapter) SetPushNameRefresher(fn func(ctx context.Context, jid domain.JID, pushName string)) {
	if a == nil {
		return
	}
	a.pushNameSink = fn
}

// extractBodyAndMedia pulls body text, MIME type, and caption from a
// whatsmeow message event. Used by both persistInboundMessage and (in a
// future commit) the history sync translator.
func extractBodyAndMedia(wmEvt *events.Message) (body, mediaType, caption string) {
	if wmEvt.Message == nil {
		return "", "", ""
	}
	if c := wmEvt.Message.GetConversation(); c != "" {
		return c, "", ""
	}
	if ext := wmEvt.Message.GetExtendedTextMessage(); ext != nil && ext.GetText() != "" {
		return ext.GetText(), "", ""
	}
	if img := wmEvt.Message.GetImageMessage(); img != nil {
		return img.GetCaption(), img.GetMimetype(), img.GetCaption()
	}
	if doc := wmEvt.Message.GetDocumentMessage(); doc != nil {
		return doc.GetCaption(), doc.GetMimetype(), doc.GetCaption()
	}
	if vid := wmEvt.Message.GetVideoMessage(); vid != nil {
		return vid.GetCaption(), vid.GetMimetype(), vid.GetCaption()
	}
	if aud := wmEvt.Message.GetAudioMessage(); aud != nil {
		return "", aud.GetMimetype(), ""
	}
	return "", "", ""
}
