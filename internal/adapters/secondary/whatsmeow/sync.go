package whatsmeow

import (
	"context"
	"fmt"
	"time"

	"github.com/yolo-labz/wa/v2/internal/domain"
)

// SyncSnapshot is a point-in-time view of the on-demand history sync
// engine's internal state. It surfaces what the health probe does not:
// whether a blob is being processed right now, how many force
// round-trips are in flight, and how deep the worker's intake queue is.
// Issue #174 (sync.status).
type SyncSnapshot struct {
	// Syncing is true while a history sync blob is actively being
	// persisted (mirrors IsSyncing). It is transient — true only for the
	// duration of one processHistorySyncBlob call.
	Syncing bool
	// InFlightForceReqs counts pending on-demand history round-trips
	// (entries in the historyReqs routing table). A non-zero value means
	// a ForceHistorySync / LoadMore caller is blocked awaiting WA's reply.
	InFlightForceReqs int
	// QueueDepth is the number of history sync blobs buffered for the
	// background worker but not yet processed.
	QueueDepth int
	// QueueCap is the worker channel capacity (historySyncChCap). When
	// QueueDepth approaches QueueCap, incoming blobs risk being dropped.
	QueueCap int
}

// SyncStatus returns a snapshot of the on-demand history sync engine's
// internal state. Safe to call concurrently; reads atomics and channel
// lengths only. Issue #174.
func (a *Adapter) SyncStatus() SyncSnapshot {
	inFlight := 0
	a.historyReqs.Range(func(_, _ any) bool {
		inFlight++
		return true
	})
	return SyncSnapshot{
		Syncing:           a.isSyncing.Load(),
		InFlightForceReqs: inFlight,
		QueueDepth:        len(a.historySyncCh),
		QueueCap:          cap(a.historySyncCh),
	}
}

// ForceSyncResult reports the outcome of a ForceHistorySync round-trip.
type ForceSyncResult struct {
	// Requested is true once the on-demand BuildHistorySyncRequest was
	// built and handed to SendMessage.
	Requested bool
	// Connected reflects whether the client was connected + logged-in
	// when the force ran. False means no request was sent.
	Connected bool
	// Chat is the target chat JID ("" = global per-chat sweep).
	Chat string
	// Count is the number of newest messages requested (capped at
	// historyRoundTripCap).
	Count int
	// Received is how many messages a matching ON_DEMAND response
	// delivered. Meaningful only for the chat-scoped path; always 0 for
	// the global (async) path.
	Received int
	// Delivered is true when a matching ON_DEMAND response arrived before
	// the deadline (chat-scoped path only).
	Delivered bool
	// TimedOut is true when the deadline elapsed with no matching
	// response (chat-scoped path only). The pull may still land later and
	// persist asynchronously — TimedOut means "no synchronous ACK", not
	// "failed".
	TimedOut bool
	// Async is true for the global (no-chat) path: the request was fired
	// and the response is persisted by the background worker without a
	// synchronous wait. Poll SyncStatus / chat.list to observe arrival.
	Async bool
}

// ForceHistorySync triggers an on-demand history pull from WhatsApp's
// servers, bypassing LoadMore's local-first short-circuit. It exists for
// the #174 symptom: new messages are visible on the phone but the
// daemon's DB has not caught up, and there is no local cursor that would
// make LoadMore reach out to the server.
//
// The underlying primitive (whatsmeow BuildHistorySyncRequest) requires a
// NON-NIL anchor message and returns `count` messages immediately BEFORE it.
// To recover a recent gap we therefore anchor at the chat's NEWEST stored
// message — re-pulling the most recent slice, which the store dedups, so
// any window missed during a stall is filled. Passing a nil anchor panics
// (whatsmeow dereferences it); that was the wa-burocracy sync crash PR #222
// fixed. The response is persisted by the background history sync worker
// regardless of whether a caller is waiting (persist-late), so the DB is
// refreshed either way.
//
// Two modes, dictated by the ON_DEMAND response routing (which matches a
// pending entry by exact chat JID — see routeOnDemandResponse):
//
//   - chat-scoped (chat is non-zero): anchor at the chat's newest stored
//     message, register a pending entry keyed by the chat JID, and block
//     until a matching conversation arrives or the deadline elapses.
//     Received reports how many messages landed. With no stored message to
//     anchor against, this is an honest no-op (Requested=false).
//   - global (chat is zero): sweep the most-recently-active chats, anchor
//     each at its newest stored message, and fire one pull per chat
//     (whatsmeow's request is per-chat — there is no single "all chats"
//     pull). Async: the worker persists; poll sync.status / chat.list.
//
// The never-leak invariant from LoadMore is preserved: the historyReqs
// entry is deleted in every terminal path.
func (a *Adapter) ForceHistorySync(ctx context.Context, chat domain.JID, count int) (ForceSyncResult, error) {
	if err := ctx.Err(); err != nil {
		return ForceSyncResult{}, err
	}
	if count <= 0 || count > historyRoundTripCap {
		count = historyRoundTripCap
	}
	res := ForceSyncResult{Chat: chat.String(), Count: count}

	// No remote transport → an honest typed error, never a silent no-op.
	// Mirrors the moderator/profile/blocker adapters' ErrDisconnected
	// contract so REST/CLI map it to the same exit code.
	if a.closed.Load() || a.client == nil || !a.client.IsConnected() || !a.client.IsLoggedIn() {
		return res, fmt.Errorf("ForceHistorySync: %w", domain.ErrDisconnected)
	}
	res.Connected = true

	if chat.IsZero() {
		return a.forceGlobalBackfill(ctx, count, res)
	}
	return a.forceChatScopedSync(ctx, chat, count, res)
}

// forceGlobalBackfill sweeps the most-recently-active chats and fires one
// on-demand pull per chat, anchored at each chat's newest stored message —
// whatsmeow's on-demand request is per-chat, so there is no single "all
// chats" pull. Async: the background worker persists the responses; this
// returns once the requests are fired. PR #222.
func (a *Adapter) forceGlobalBackfill(ctx context.Context, count int, res ForceSyncResult) (ForceSyncResult, error) {
	chats, err := a.recentChats(ctx, globalBackfillChats)
	if err != nil {
		return res, fmt.Errorf("ForceHistorySync: recent chats: %w", err)
	}
	for _, c := range chats {
		ref, ok, rerr := a.newestRef(ctx, c)
		if rerr != nil || !ok {
			continue
		}
		a.sendHistoryRequest(anchorFromRef(ref), count)
		res.Requested = true
	}
	res.Async = true
	return res, nil
}

// forceChatScopedSync anchors at the chat's newest stored message, registers
// a pending entry, fires the pull, and blocks for a matching ON_DEMAND
// response (or the deadline). With no stored message to anchor against it is
// an honest no-op (Requested stays false) rather than a panic or a silent
// lie. The never-leak invariant holds: the pending entry is deleted in every
// terminal path. PR #222.
func (a *Adapter) forceChatScopedSync(ctx context.Context, chat domain.JID, count int, res ForceSyncResult) (ForceSyncResult, error) {
	ref, ok, err := a.newestRef(ctx, chat)
	if err != nil {
		return res, fmt.Errorf("ForceHistorySync: anchor: %w", err)
	}
	if !ok {
		return res, nil
	}

	seq := historyReqSeq(a.historyReqCounter.Add(1))
	pending := &pendingHistoryReq{chatJID: chat.String(), msgs: make(chan []domain.Message, 1)}
	a.historyReqs.Store(seq, pending)
	defer a.historyReqs.Delete(seq) // never-leak invariant

	a.sendHistoryRequest(anchorFromRef(ref), count)
	res.Requested = true

	timeout := a.forceSyncTimeout
	if timeout <= 0 {
		timeout = historyRequestTimeout
	}
	timer := time.NewTimer(timeout)
	defer timer.Stop()

	select {
	case remote := <-pending.msgs:
		// The worker already persisted these via persistConversation before
		// routing here, so we do not re-insert — Received is the ACK that the
		// DB was refreshed for this chat.
		res.Delivered = true
		res.Received = len(remote)
		return res, nil
	case <-timer.C:
		res.TimedOut = true
		return res, nil
	case <-ctx.Done():
		return res, ctx.Err()
	case <-a.clientCtx.Done():
		return res, nil
	}
}
