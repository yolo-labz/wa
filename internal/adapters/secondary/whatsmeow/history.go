package whatsmeow

import (
	"context"
	"fmt"
	"strconv"
	"sync/atomic"
	"time"

	waTypes "go.mau.fi/whatsmeow/types"

	"github.com/yolo-labz/wa/v2/internal/domain"
)

// historyRoundTripCap bounds a single BuildHistorySyncRequest per the
// contracts/historystore.md §HS2 hint: whatsmeow's on-demand sync is
// expensive and the daemon should chunk requests. The cap matches the
// research §D1 recommendation.
const historyRoundTripCap = 50

// globalBackfillChats caps how many recently-active chats a global (no-chat)
// ForceHistorySync sweeps — one on-demand pull each, since whatsmeow's
// request is per-chat. Bounds the request burst after a stall recovery.
// PR #222.
const globalBackfillChats = 50

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
	chatJID string // target chat for matching ON_DEMAND responses
	msgs    chan []domain.Message
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
//
//nolint:gocyclo // translation fan-out across local/remote/timeout/ctx paths; splitting hurts readability
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

	remaining := min(limit-len(local), historyRoundTripCap)

	// Anchor the remote pull at the oldest message we hold for this chat; WA
	// returns `remaining` messages older than it (scroll-up). The anchor is
	// nil when the chat has no stored message (a genuinely un-anchorable pull)
	// — sendHistoryRequest treats nil as a no-op, never the panic that took
	// down wa-burocracy. The pending entry is still registered so a delivered
	// response is routed. PR #222.
	anchor := a.oldestAnchor(ctx, chat)

	seq := historyReqSeq(atomic.AddUint64(&historyReqSeqCounter, 1))
	pending := &pendingHistoryReq{chatJID: chat.String(), msgs: make(chan []domain.Message, 1)}
	a.historyReqs.Store(seq, pending)
	// Never-leak invariant: delete in EVERY terminal path.
	defer a.historyReqs.Delete(seq)

	a.sendHistoryRequest(anchor, remaining)

	// Step 4: await the response or a terminal condition.
	timer := time.NewTimer(historyRequestTimeout)
	defer timer.Stop()

	select {
	case remote := <-pending.msgs:
		// Step 5: persist-late. Write freshly-received messages to the
		// local store before returning them so a subsequent LoadMore
		// call can serve from local storage (HS6).
		var insertErr error
		if a.history != nil && len(remote) > 0 {
			if err := a.history.InsertDomainMessages(ctx, remote); err != nil {
				insertErr = err
				a.recordAuditDetail(domain.AuditPanic, chat, "history_insert", err.Error())
			}
		}
		// R-08: emit AuditHistoryComplete on normal completion only
		// (persist failure routes to AuditPanic above, never to
		// AuditHistoryComplete — the two decisions are mutually
		// exclusive so downstream consumers can trust the signal).
		if insertErr == nil {
			a.recordAuditDetail(
				domain.AuditHistoryComplete, chat, "ok",
				"remote="+strconv.Itoa(len(remote))+" local="+strconv.Itoa(len(local)),
			)
		}
		combined := make([]domain.Message, 0, len(local)+len(remote))
		combined = append(combined, local...)
		combined = append(combined, remote...)
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

// anchorFromRef converts a stored-message reference into the whatsmeow
// MessageInfo that BuildHistorySyncRequest requires. The on-demand request
// carries the anchor's chat / id / direction / timestamp and asks WhatsApp
// for `count` messages immediately before it. PR #222.
func anchorFromRef(ref domain.MessageRef) *waTypes.MessageInfo {
	return &waTypes.MessageInfo{
		MessageSource: waTypes.MessageSource{
			Chat:     toWhatsmeow(ref.Chat),
			IsFromMe: ref.FromMe,
		},
		ID:        waTypes.MessageID(ref.ID),
		Timestamp: ref.Timestamp,
	}
}

// newestRef / oldestRef / recentChats wrap the history container with a
// nil-guard so the on-demand paths degrade to "no anchor / no chats" (a
// clean skip) when no history store is wired — unit tests, or after a Panic
// wipe sets a.history = nil — instead of nil-dereferencing. PR #222.
func (a *Adapter) newestRef(ctx context.Context, chat domain.JID) (domain.MessageRef, bool, error) {
	if a.history == nil {
		return domain.MessageRef{}, false, nil
	}
	return a.history.NewestRef(ctx, chat)
}

func (a *Adapter) oldestRef(ctx context.Context, chat domain.JID) (domain.MessageRef, bool, error) {
	if a.history == nil {
		return domain.MessageRef{}, false, nil
	}
	return a.history.OldestRef(ctx, chat)
}

func (a *Adapter) recentChats(ctx context.Context, limit int) ([]domain.JID, error) {
	if a.history == nil {
		return nil, nil
	}
	return a.history.RecentChats(ctx, limit)
}

// oldestAnchor resolves the oldest-message anchor for a LoadMore remote pull,
// or nil when the chat is un-anchorable (empty, or no container wired). A
// lookup error is logged, not swallowed (CLAUDE.md rule 12), and degrades to
// a nil anchor — sendHistoryRequest then skips the pull. PR #222.
func (a *Adapter) oldestAnchor(ctx context.Context, chat domain.JID) *waTypes.MessageInfo {
	ref, ok, err := a.oldestRef(ctx, chat)
	if err != nil {
		if a.logger != nil {
			a.logger.Warn("LoadMore: oldest-anchor lookup failed; remote pull skipped",
				"error", err, "chat", chat.String())
		}
		return nil
	}
	if !ok {
		return nil
	}
	return anchorFromRef(ref)
}

// sendHistoryRequest builds an on-demand history request anchored at the
// given message and sends it to the user's own JID. Best-effort: errors are
// dropped because the response arrives asynchronously on a separate path.
//
// A nil anchor is a no-op, NOT a panic. whatsmeow's BuildHistorySyncRequest
// dereferences the anchor unconditionally (send.go fills ChatJID/OldestMsgID
// from it), so the historical nil argument crashed the request. Callers now
// resolve a real anchor first and skip the pull when a chat has no stored
// message to anchor against.
//
// The whatsmeow interaction is wrapped in a recover: BuildHistorySyncRequest
// reaches into library internals, and this also runs on the soft-stale
// watchdog's backfill goroutine (PR #221), which has no dispatcher panic
// recovery above it — there, an unrecovered panic would take down the daemon.
func (a *Adapter) sendHistoryRequest(anchor *waTypes.MessageInfo, count int) {
	if anchor == nil {
		if a.logger != nil {
			a.logger.Debug("history request skipped: no anchor message available")
		}
		return
	}
	defer func() {
		if r := recover(); r != nil {
			if a.logger != nil {
				a.logger.Error("history request panicked (recovered)", "panic", r)
			}
			a.recordAuditDetail(domain.AuditPanic, domain.JID{}, "history_request_panic", fmt.Sprintf("%v", r))
		}
	}()

	req := a.client.BuildHistorySyncRequest(anchor, count)
	if req == nil {
		return
	}
	if !a.client.IsLoggedIn() {
		return
	}
	device := a.client.Store()
	if device == nil || device.ID == nil {
		return
	}
	_, _ = a.client.SendMessage(a.clientCtx, *device.ID, req)
}

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
