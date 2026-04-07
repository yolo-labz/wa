package whatsmeow

import (
	"context"
	"fmt"
	"strconv"
	"sync/atomic"
	"time"

	"github.com/yolo-labz/wa/internal/domain"
)

// historyRoundTripCap bounds a single BuildHistorySyncRequest per the
// contracts/historystore.md §HS2 hint: whatsmeow's on-demand sync is
// expensive and the daemon should chunk requests. The cap matches the
// research §D1 recommendation.
const historyRoundTripCap = 50

// historyRequestTimeout is the per-round-trip deadline. A request that
// does not yield a HistorySync response within this window is considered
// lost and the LoadMore caller receives whatever local/remote results
// have already accumulated (or an empty slice if none). Per Clarifications
// round 2 Q1 the sync.Map entry MUST be deleted before LoadMore returns
// in every terminal path — the never-leak invariant.
const historyRequestTimeout = 30 * time.Second

// historyReqSeq uniquely identifies an in-flight BuildHistorySyncRequest
// within a single Adapter instance. It wraps handleHistorySync's routing
// table keys and is local to the adapter; whatsmeow's own request ID is
// not exposed here.
type historyReqSeq uint64

// pendingHistoryReq is the value stored in a.historyReqs under a
// historyReqSeq key. The caller of LoadMore blocks on msgs; the event
// handler (when plumbed) writes the translated messages into it.
type pendingHistoryReq struct {
	msgs chan []domain.Message
}

// LoadMore implements app.HistoryStore per contracts/historystore.md
// (clauses HS1–HS6) and Clarifications round 2 Q1 (persist-late
// never-leak). The flow is:
//
//  1. (HS5) reject limit ≤ 0 and zero chat JID with a typed error.
//  2. (HS1) query the local historyContainer for up to `limit` messages
//     older than `before`. If it returns ≥ limit, return immediately.
//  3. (HS2) build a BuildHistorySyncRequest for the remainder (capped at
//     historyRoundTripCap), register a pendingHistoryReq keyed by a
//     monotonic historyReqSeq in a.historyReqs, and SendMessage to the
//     user's own phone.
//  4. Select on the pending channel, historyRequestTimeout, and
//     ctx.Done() / clientCtx.Done(). In every terminal path the
//     sync.Map entry is deleted before returning — the never-leak
//     invariant.
//  5. (HS6) persist any freshly-received remote messages via
//     a.history.Insert before returning.
//
// In commit 4 the event-handler side of the routing is stubbed (the
// HistorySync case in handleWAEvent does not yet write into
// pendingHistoryReq.msgs); the full wiring arrives in a later commit.
// Tests that exercise LoadMore feed the fake client and inject results
// directly via the test-only resolveHistoryReq helper below.
func (a *Adapter) LoadMore(ctx context.Context, chat domain.JID, before domain.MessageID, limit int) ([]domain.Message, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if chat.IsZero() {
		return nil, fmt.Errorf("HistoryStore.LoadMore: %w", domain.ErrInvalidJID)
	}
	if limit <= 0 {
		return nil, fmt.Errorf("HistoryStore.LoadMore: limit must be > 0, got %d", limit)
	}

	// Step 2: local-first read.
	local, err := a.loadLocal(ctx, chat, before, limit)
	if err != nil {
		return nil, fmt.Errorf("HistoryStore.LoadMore: local: %w", err)
	}
	if len(local) >= limit {
		return local[:limit], nil
	}

	// Step 3: if there is no remote transport (closed, disconnected, or
	// history stub is nil) we return what we have. HS3 says empty is a
	// success case.
	if a.closed.Load() || a.client == nil || !a.client.IsConnected() {
		return local, nil
	}

	remaining := limit - len(local)
	if remaining > historyRoundTripCap {
		remaining = historyRoundTripCap
	}

	seq := historyReqSeq(atomic.AddUint64(&historyReqSeqCounter, 1))
	pending := &pendingHistoryReq{msgs: make(chan []domain.Message, 1)}
	a.historyReqs.Store(seq, pending)
	// Never-leak invariant: delete in EVERY terminal path.
	defer a.historyReqs.Delete(seq)

	// Build the on-demand request and send it to the user's own JID
	// (whatsmeow's API delivers it via SendMessage to self). The
	// lastKnownMessageInfo argument is nil in commit 4 — richer cursor
	// semantics arrive with commit 7's messages.db.
	req := a.client.BuildHistorySyncRequest(nil, remaining)
	if req != nil {
		// Best-effort send-to-self. We do not use caller ctx here
		// because the response arrives asynchronously on a separate
		// path; the caller ctx governs the select below instead.
		if a.client.IsLoggedIn() {
			device := a.client.Store()
			if device != nil && device.ID != nil {
				_, _ = a.client.SendMessage(a.clientCtx, *device.ID, req)
			}
		}
	}

	// Step 4: await the response or a terminal condition.
	timer := time.NewTimer(historyRequestTimeout)
	defer timer.Stop()

	select {
	case remote := <-pending.msgs:
		// Step 5: persist-late. Write freshly-received messages to the
		// local store before returning them so a subsequent LoadMore
		// call can serve from local storage (HS6).
		if a.history != nil && len(remote) > 0 {
			if err := a.history.Insert(ctx, remote); err != nil {
				a.recordAuditDetail(domain.AuditPanic, chat, "history_insert", err.Error())
			}
		}
		combined := append(local, remote...)
		if len(combined) > limit {
			combined = combined[:limit]
		}
		return combined, nil

	case <-timer.C:
		a.recordAuditDetail(domain.AuditPanic, chat, "history_timeout", strconv.FormatUint(uint64(seq), 10))
		return local, nil

	case <-ctx.Done():
		return nil, ctx.Err()

	case <-a.clientCtx.Done():
		return local, nil
	}
}

// historyReqSeqCounter is the package-scoped monotonic counter backing
// historyReqSeq allocation. Using a package var rather than a field on
// Adapter keeps the sync.Map key type comparable across tests.
var historyReqSeqCounter uint64

// loadLocal reads from a.history if wired, or from the test overlay
// seedHistory map otherwise. Returns newest-first and capped at limit.
// The `before` cursor is honoured only in the local path; the remote
// on-demand path is driven by BuildHistorySyncRequest's own cursor.
func (a *Adapter) loadLocal(ctx context.Context, chat domain.JID, before domain.MessageID, limit int) ([]domain.Message, error) {
	if a.history != nil {
		return a.history.LoadMore(ctx, chat, before, limit)
	}
	a.overlayMu.Lock()
	defer a.overlayMu.Unlock()
	src := a.seedHistory[chat]
	out := make([]domain.Message, 0, len(src))
	for i := len(src) - 1; i >= 0; i-- {
		if len(out) >= limit {
			break
		}
		out = append(out, src[i])
	}
	return out, nil
}

// resolveHistoryReq is the test-only helper that simulates a completed
// BuildHistorySyncRequest. It looks up the most recently registered
// pending request and delivers msgs to it. Used by history_test.go to
// exercise the HS2/HS6 clauses against the fake client without a real
// HistorySync protobuf round-trip.
func (a *Adapter) resolveHistoryReq(msgs []domain.Message) bool {
	var delivered bool
	a.historyReqs.Range(func(key, value any) bool {
		if pending, ok := value.(*pendingHistoryReq); ok {
			select {
			case pending.msgs <- msgs:
				delivered = true
			default:
			}
		}
		return !delivered
	})
	return delivered
}
