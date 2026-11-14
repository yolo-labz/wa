package whatsmeow

import (
	"context"
	"errors"
	"fmt"
	"time"

	waTypes "go.mau.fi/whatsmeow/types"

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
	// GapReachable (FR-116-25) is nil when no recovery gap is tracked;
	// otherwise it says whether the pull can address the gap window. It
	// never claims missing messages were restored. Reason is the machine code.
	GapReachable *bool
	Reason       string
	// Budget is "queued" or "exhausted" when the pull budget withheld it.
	Budget string
	// Chats reports reachability per chat for a global sweep.
	Chats []ChatSync
}

// sweepWait bounds how long a global sweep waits on the pull budget.
const sweepWait = 2 * time.Minute

// SweepCounts reports how many sweep chats were requested and how many the
// pull budget withheld ("queued" or "exhausted").
func (r ForceSyncResult) SweepCounts() (requested, withheld int) {
	for _, c := range r.Chats {
		if c.Requested {
			requested++
		} else if c.Budget != "" {
			withheld++
		}
	}
	return requested, withheld
}

// ChatSync is one chat's outcome within a global sweep.
type ChatSync struct {
	Chat         string `json:"chat"`
	Requested    bool   `json:"requested"`
	GapReachable *bool  `json:"gap_reachable,omitempty"`
	Reason       string `json:"reason,omitempty"`
	Budget       string `json:"budget,omitempty"`
}

// FR-116-25 reachability reasons.
const (
	ReasonPostRecoveryAnchor = "post_recovery_anchor"
	ReasonTailGapUnanchored  = "tail_gap_unanchored"
	ReasonNoAnchor           = "no_anchor"
	ReasonGapStateUnknown    = "gap_state_unknown"
	ReasonNoPendingGap       = "no_pending_gap"
)

// RecoveryEvidence is the FR-116-24 bookkeeping reachability reads.
type RecoveryEvidence interface {
	RecoveryEpisodes() []domain.RecoveryEpisode
}

// SetRecoveryEvidence enables FR-116-25 reachability for force syncs.
func (a *Adapter) SetRecoveryEvidence(ev RecoveryEvidence) { a.recoveryEv.Store(&ev) }

type gapView struct {
	tracked bool
	eps     map[string]domain.RecoveryEpisode
}

func (a *Adapter) gapView() gapView {
	p, _ := a.recoveryEv.Load().(*RecoveryEvidence)
	if p == nil {
		return gapView{}
	}
	eps := (*p).RecoveryEpisodes()
	v := gapView{tracked: true, eps: make(map[string]domain.RecoveryEpisode, len(eps))}
	for _, e := range eps {
		v.eps[e.ChatJID] = e
	}
	return v
}

// plan resolves one chat's anchor and verdict from that chat's own episode;
// a nil anchor means no request. Only a real post-recovery live anchor makes a
// gap reachable. An unanchored tail gap is never pulled from the pre-outage
// newest row unless the caller explicitly asked for older history, which keeps
// the false verdict. A chat with no episode pulls before its newest row.
func (a *Adapter) plan(ctx context.Context, v gapView, chat domain.JID, older bool) (ChatSync, *waTypes.MessageInfo, error) {
	cs := ChatSync{Chat: chat.String()}
	ref, ok, err := a.newestRef(ctx, chat)
	if err != nil || !v.tracked {
		if err != nil || !ok {
			return cs, nil, err
		}
		return cs, anchorFromRef(ref), nil
	}
	e, pending := v.eps[chat.String()]
	reachable, unreachable := true, false
	switch {
	case pending && e.AnchorID != "":
		cs.GapReachable, cs.Reason = &reachable, ReasonPostRecoveryAnchor
		return cs, anchorFromRef(domain.MessageRef{Chat: chat, ID: domain.MessageID(e.AnchorID), FromMe: e.AnchorFromMe, Timestamp: time.Unix(e.AnchorTS, 0)}), nil
	case !pending:
		cs.Reason = ReasonNoPendingGap
		if !ok {
			return cs, nil, nil
		}
		return cs, anchorFromRef(ref), nil
	case !ok:
		cs.GapReachable, cs.Reason = &unreachable, ReasonNoAnchor
		return cs, nil, nil
	}
	cs.GapReachable, cs.Reason = &unreachable, ReasonTailGapUnanchored
	if e.Unknown {
		cs.Reason = ReasonGapStateUnknown
	}
	if older {
		return cs, anchorFromRef(ref), nil
	}
	return cs, nil, nil
}

// Reachability is ForceSync's FR-116-25 verdict without its pull, which a
// job answer carries before the work ends: one verdict for a chat, one per
// recent chat for a sweep. A store that cannot answer is false with
// gap_state_unknown, never optimistic.
func (a *Adapter) Reachability(ctx context.Context, chat domain.JID, older bool) ForceSyncResult {
	v := a.gapView()
	no := false
	unknown := ForceSyncResult{GapReachable: &no, Reason: ReasonGapStateUnknown}
	if !chat.IsZero() {
		cs, _, err := a.plan(ctx, v, chat, older)
		if err != nil {
			return unknown
		}
		return ForceSyncResult{GapReachable: cs.GapReachable, Reason: cs.Reason}
	}
	chats, err := a.sweepChats(ctx)
	if err != nil {
		return unknown
	}
	var res ForceSyncResult
	for _, c := range chats {
		cs, _, perr := a.plan(ctx, v, c, older)
		if perr != nil {
			cs = unknownChat(c)
		}
		res.Chats = append(res.Chats, cs)
	}
	return res
}

type chatFilterKey struct{}

// WithChatFilter narrows a sweep (ForceSync or Reachability with no chat) to
// the chats allow accepts, so a caller's policy decides which chats the
// daemon may pull history for. A chat-scoped call is the caller's to gate.
func WithChatFilter(ctx context.Context, allow func(domain.JID) bool) context.Context {
	return context.WithValue(ctx, chatFilterKey{}, allow)
}

// ChatFilter is the filter WithChatFilter put on ctx, or nil.
func ChatFilter(ctx context.Context) func(domain.JID) bool {
	allow, _ := ctx.Value(chatFilterKey{}).(func(domain.JID) bool)
	return allow
}

// sweepChats is the recent-chat list a sweep walks, cut to the ctx's filter.
// A filtered sweep reads a wider pool first, so chats the filter drops do not
// crowd out the ones it keeps.
func (a *Adapter) sweepChats(ctx context.Context) ([]domain.JID, error) {
	allow := ChatFilter(ctx)
	if allow == nil {
		return a.recentChats(ctx, globalBackfillChats)
	}
	pool, err := a.recentChats(ctx, 4*globalBackfillChats)
	if err != nil {
		return nil, err
	}
	var out []domain.JID
	for _, c := range pool {
		if allow(c) {
			if out = append(out, c); len(out) == globalBackfillChats {
				break
			}
		}
	}
	return out, nil
}

// unknownChat is a sweep entry for a chat whose plan failed: false and
// gap_state_unknown, never absent and never optimistic.
func unknownChat(c domain.JID) ChatSync {
	no := false
	return ChatSync{Chat: c.String(), GapReachable: &no, Reason: ReasonGapStateUnknown}
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
	return a.ForceSync(ctx, chat, count, false)
}

// ForceSync is ForceHistorySync with FR-116-25's explicit older-history flag.
func (a *Adapter) ForceSync(ctx context.Context, chat domain.JID, count int, olderHistory bool) (ForceSyncResult, error) {
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

	v := a.gapView()
	if chat.IsZero() {
		return a.forceGlobalBackfill(ctx, v, count, olderHistory, res)
	}
	return a.forceChatScopedSync(ctx, v, chat, count, olderHistory, res)
}

// forceGlobalBackfill sweeps the most-recently-active chats and fires one
// on-demand pull per chat — whatsmeow's on-demand request is per-chat, so
// there is no single "all chats" pull. Async: the background worker
// persists the responses. Each chat reports its own reachability and waits
// its FIFO turn in the pull budget for at most sweepWait (or the caller's
// earlier deadline); chats still waiting then report "exhausted".
func (a *Adapter) forceGlobalBackfill(ctx context.Context, v gapView, count int, older bool, res ForceSyncResult) (ForceSyncResult, error) {
	chats, err := a.sweepChats(ctx)
	if err != nil {
		return res, fmt.Errorf("ForceHistorySync: recent chats: %w", err)
	}
	wait, cancel := context.WithTimeout(ctx, sweepWait)
	defer cancel()
	for _, c := range chats {
		cs, anchor, perr := a.plan(ctx, v, c, older)
		if perr != nil {
			res.Chats = append(res.Chats, unknownChat(c))
			continue
		}
		if anchor != nil {
			release, aerr := a.pulls.acquire(wait, c.String())
			switch {
			case aerr == nil:
				a.sendHistoryRequest(ctx, anchor, count)
				release()
				cs.Requested, res.Requested = true, true
			case errors.Is(aerr, errPullQueued):
				cs.Budget = "queued"
			default:
				cs.Budget = "exhausted"
			}
		}
		res.Chats = append(res.Chats, cs)
	}
	res.Async = true
	return res, nil
}

// forceChatScopedSync resolves the chat's anchor and verdict, waits its turn
// in the pull budget, then pulls. With no usable anchor it is an honest
// no-op (Requested stays false) rather than a panic or a silent lie.
func (a *Adapter) forceChatScopedSync(ctx context.Context, v gapView, chat domain.JID, count int, older bool, res ForceSyncResult) (ForceSyncResult, error) {
	cs, anchor, err := a.plan(ctx, v, chat, older)
	if err != nil {
		return res, fmt.Errorf("ForceHistorySync: anchor: %w", err)
	}
	res.GapReachable, res.Reason = cs.GapReachable, cs.Reason
	if anchor == nil {
		return res, nil
	}
	release, err := a.pulls.acquire(ctx, chat.String())
	switch {
	case errors.Is(err, errPullQueued):
		res.Budget = "queued"
		return res, nil
	case errors.Is(err, errPullBudget):
		res.Budget = "exhausted"
		return res, nil
	case err != nil:
		return res, err
	}
	defer release()
	return a.pullBefore(ctx, chat, anchor, count, res)
}

// pullBefore registers a pending entry, fires one pull for `count` messages
// before anchor, and blocks for a matching ON_DEMAND response (or the
// deadline). The never-leak invariant holds: the pending entry is deleted in
// every terminal path. PR #222.
func (a *Adapter) pullBefore(ctx context.Context, chat domain.JID, anchor *waTypes.MessageInfo, count int, res ForceSyncResult) (ForceSyncResult, error) {
	seq := historyReqSeq(a.historyReqCounter.Add(1))
	pending := &pendingHistoryReq{chatJID: chat.String(), msgs: make(chan []domain.Message, 1)}
	a.historyReqs.Store(seq, pending)
	defer a.historyReqs.Delete(seq) // never-leak invariant

	a.sendHistoryRequest(ctx, anchor, count)
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
