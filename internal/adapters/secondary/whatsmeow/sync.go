package whatsmeow

import (
	"context"
	"fmt"
	"sync/atomic"
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
	// Chat is the target chat JID ("" = global newest-N pull).
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
// The underlying primitive (BuildHistorySyncRequest(nil, count)) asks for
// the newest `count` messages globally — exactly the right direction for
// "I have new messages the daemon is missing" (anchoring to a stored
// message would fetch OLDER history instead). The response is persisted
// by the background history sync worker regardless of whether a caller is
// waiting (persist-late), so the DB is refreshed either way.
//
// Two modes, dictated by the ON_DEMAND response routing (which matches a
// pending entry by exact chat JID — see routeOnDemandResponse):
//
//   - chat-scoped (chat is non-zero): register a pending entry keyed by
//     the chat JID and block until a matching conversation arrives or the
//     deadline elapses. This is the synchronous "block until the daemon
//     ACKs" path #174 asks for. Received reports how many messages landed
//     for that chat.
//   - global (chat is zero): there is no single chat JID to match a
//     pending entry against, so the request is fired and left to the
//     worker to persist asynchronously (Async=true). The operator polls
//     sync.status / chat.list to observe the refresh.
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

	// Global path: fire and let the worker persist asynchronously.
	if chat.IsZero() {
		a.sendHistoryRequest(count)
		res.Requested = true
		res.Async = true
		return res, nil
	}

	// Chat-scoped path: register a pending entry, fire, block for the ACK.
	seq := historyReqSeq(atomic.AddUint64(&historyReqSeqCounter, 1))
	pending := &pendingHistoryReq{chatJID: chat.String(), msgs: make(chan []domain.Message, 1)}
	a.historyReqs.Store(seq, pending)
	defer a.historyReqs.Delete(seq) // never-leak invariant

	a.sendHistoryRequest(count)
	res.Requested = true

	timeout := a.forceSyncTimeout
	if timeout <= 0 {
		timeout = historyRequestTimeout
	}
	timer := time.NewTimer(timeout)
	defer timer.Stop()

	select {
	case remote := <-pending.msgs:
		// The worker already persisted these via persistConversation
		// before routing here, so we do not re-insert — Received is the
		// ACK that the DB was refreshed for this chat.
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
