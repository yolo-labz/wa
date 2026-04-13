package whatsmeow

import (
	"context"
	"log/slog"

	waCommon "go.mau.fi/whatsmeow/proto/waCommon"
	waHistorySync "go.mau.fi/whatsmeow/proto/waHistorySync"
	waWeb "go.mau.fi/whatsmeow/proto/waWeb"
	"go.mau.fi/whatsmeow/types/events"

	"github.com/yolo-labz/wa/internal/domain"
)

// historySyncChCap is the bounded channel capacity for the history sync
// processing goroutine. Sized to absorb a typical initial sync burst of
// 3–5 blobs with headroom. Feature 009 — spec FR-026, research D1.
const historySyncChCap = 8

// IsSyncing reports whether a history sync blob is currently being
// processed. Used by the retention cleanup goroutine to avoid bulk
// deletes during active sync. Feature 009 — edge case spec.
func (a *Adapter) IsSyncing() bool { return a.isSyncing.Load() }

// dispatchHistorySync sends a raw history sync event to the background
// processing goroutine. Non-blocking: if the channel is full, the event
// is dropped and an audit entry is logged. Feature 009 — FR-009.
func (a *Adapter) dispatchHistorySync(rawEvt any) {
	select {
	case a.historySyncCh <- rawEvt:
	default:
		a.recordAuditDetail(domain.AuditPanic, domain.JID{}, "hsync_ch_full", "history sync channel full, blob dropped")
	}
}

// runHistorySyncWorker is the background goroutine that processes
// history sync blobs. It reads from a.historySyncCh and for each blob:
// 1. Type-asserts to *events.HistorySync
// 2. Determines SyncType; drops FULL/INITIAL_STATUS_V3
// 3. Extracts messages from the decoded protobuf Data field
// 4. Batch-inserts via historyContainer.InsertRaw
// 5. For ON_DEMAND, routes to historyReqs if a pending entry exists
//
// Feature 009 — FR-002, FR-006, FR-008, FR-009, FR-010, FR-011, FR-012.
func (a *Adapter) runHistorySyncWorker(ctx context.Context) {
	defer a.historySyncWg.Done()

	for {
		select {
		case <-ctx.Done():
			return
		case rawEvt, ok := <-a.historySyncCh:
			if !ok {
				return
			}
			a.processHistorySyncBlob(ctx, rawEvt)
		}
	}
}

func (a *Adapter) processHistorySyncBlob(ctx context.Context, rawEvt any) {
	hsEvt, ok := rawEvt.(*events.HistorySync)
	if !ok || hsEvt == nil || hsEvt.Data == nil {
		return
	}
	a.isSyncing.Store(true)
	defer a.isSyncing.Store(false)

	syncType := hsEvt.Data.GetSyncType()

	// FR-008: drop FULL and INITIAL_STATUS_V3
	switch syncType {
	case waHistorySync.HistorySync_FULL,
		waHistorySync.HistorySync_INITIAL_STATUS_V3,
		waHistorySync.HistorySync_NON_BLOCKING_DATA:
		a.logger.Debug("history sync: dropping sync type", slog.String("type", syncType.String()))
		return
	case waHistorySync.HistorySync_PUSH_NAME:
		// PUSH_NAME is processed for contact names but has no messages to persist.
		a.logger.Debug("history sync: push_name received", slog.Int("count", len(hsEvt.Data.GetPushnames())))
		return
	}

	// Process INITIAL_BOOTSTRAP, RECENT, ON_DEMAND
	conversations := hsEvt.Data.GetConversations()
	a.logger.Info("history sync: processing",
		slog.String("type", syncType.String()),
		slog.Int("conversations", len(conversations)),
	)

	totalInserted := 0
	for _, conv := range conversations {
		chatJID := conv.GetID()
		if chatJID == "" {
			continue
		}
		totalInserted += a.persistConversation(ctx, chatJID, conv)

		if syncType == waHistorySync.HistorySync_ON_DEMAND {
			a.routeOnDemandResponse(chatJID, conv)
		}
	}

	a.logger.Info("history sync: complete",
		slog.String("type", syncType.String()),
		slog.Int("inserted", totalInserted),
	)
	a.recordAuditDetail(domain.AuditPanic, domain.JID{}, "hsync_complete",
		syncType.String()+" "+slog.IntValue(totalInserted).String())
}

// persistConversation inserts all messages from a single conversation
// into messages.db. Returns the number of successfully inserted messages.
func (a *Adapter) persistConversation(ctx context.Context, chatJID string, conv *waHistorySync.Conversation) int {
	msgs := conv.GetMessages()
	if len(msgs) == 0 {
		return 0
	}
	inserted := 0
	for _, hsMsg := range msgs {
		if a.persistOneMessage(ctx, chatJID, hsMsg) {
			inserted++
		}
	}
	return inserted
}

// persistOneMessage inserts a single history sync message into messages.db.
// Returns true if insertion succeeded.
func (a *Adapter) persistOneMessage(ctx context.Context, chatJID string, hsMsg *waHistorySync.HistorySyncMsg) bool {
	wmInfo := hsMsg.GetMessage()
	if wmInfo == nil {
		return false
	}
	key := wmInfo.GetKey()
	if key == nil || key.GetID() == "" {
		return false
	}

	senderJID := a.resolveSender(chatJID, key)
	ts := int64(wmInfo.GetMessageTimestamp()) //nolint:gosec // unix timestamp fits int64
	body, mediaType, caption := extractHistorySyncMessageContent(wmInfo)

	if err := a.history.InsertRaw(ctx,
		chatJID, senderJID, key.GetID(), ts,
		body, mediaType, caption, wmInfo.GetPushName(), key.GetFromMe(),
	); err != nil {
		a.recordAuditDetail(domain.AuditPanic, domain.JID{}, "hsync_insert", err.Error())
		return false
	}
	return true
}

// resolveSender determines the sender JID for a history sync message key.
func (a *Adapter) resolveSender(chatJID string, key *waCommon.MessageKey) string {
	if p := key.GetParticipant(); p != "" {
		return p
	}
	if key.GetFromMe() {
		if dev := a.client.Store(); dev != nil && dev.ID != nil {
			return dev.ID.String()
		}
	}
	return chatJID
}

// extractHistorySyncMessageContent pulls body, media type, and caption
// from a WebMessageInfo's inner Message protobuf.
func extractHistorySyncMessageContent(wmInfo *waWeb.WebMessageInfo) (body, mediaType, caption string) {
	msg := wmInfo.GetMessage()
	if msg == nil {
		return "", "", ""
	}
	if c := msg.GetConversation(); c != "" {
		return c, "", ""
	}
	if ext := msg.GetExtendedTextMessage(); ext != nil && ext.GetText() != "" {
		return ext.GetText(), "", ""
	}
	if img := msg.GetImageMessage(); img != nil {
		return img.GetCaption(), img.GetMimetype(), img.GetCaption()
	}
	if doc := msg.GetDocumentMessage(); doc != nil {
		return doc.GetCaption(), doc.GetMimetype(), doc.GetCaption()
	}
	if vid := msg.GetVideoMessage(); vid != nil {
		return vid.GetCaption(), vid.GetMimetype(), vid.GetCaption()
	}
	if aud := msg.GetAudioMessage(); aud != nil {
		return "", aud.GetMimetype(), ""
	}
	return "", "", ""
}

// routeOnDemandResponse delivers decoded messages to a pending LoadMore
// caller via historyReqs. Always persists to messages.db regardless of
// whether a pending entry exists (persist-late, never-leak). FR-011/012.
func (a *Adapter) routeOnDemandResponse(chatJID string, conv *waHistorySync.Conversation) {
	// Build []domain.Message for the pending channel
	var domainMsgs []domain.Message
	for _, hsMsg := range conv.GetMessages() {
		wmInfo := hsMsg.GetMessage()
		if wmInfo == nil || wmInfo.GetKey() == nil {
			continue
		}
		body, _, _ := extractHistorySyncMessageContent(wmInfo)
		jid, err := domain.Parse(chatJID)
		if err != nil {
			continue
		}
		domainMsgs = append(domainMsgs, domain.TextMessage{
			Recipient: jid,
			Body:      body,
		})
	}

	if len(domainMsgs) == 0 {
		return
	}

	// Route to the pending historyReqs entry matching this chat JID
	a.historyReqs.Range(func(key, value any) bool {
		pending, ok := value.(*pendingHistoryReq)
		if !ok || pending.chatJID != chatJID {
			return true // skip non-matching entries
		}
		select {
		case pending.msgs <- domainMsgs:
		default:
		}
		return false // delivered
	})
}
