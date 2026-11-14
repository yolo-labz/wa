package main

import (
	"context"
	"encoding/json"

	wmAdapter "github.com/yolo-labz/wa/v2/internal/adapters/secondary/whatsmeow"
	"github.com/yolo-labz/wa/v2/internal/app"
	"github.com/yolo-labz/wa/v2/internal/domain"
)

// registerSyncMethods registers the sync.force and sync.status JSON-RPC
// methods (#174). They expose the on-demand history sync engine that
// otherwise has no RPC surface: sync.force triggers an immediate pull
// from WhatsApp's servers (the daemon DB can lag behind the phone when
// new messages arrive), and sync.status surfaces the engine's internal
// state — in-flight force round-trips and the worker's intake queue
// depth — that the health probe deliberately does not.
//
// adapter is the concrete whatsmeow transport; it is always non-nil at
// the call site (Open failure is fatal in run()).
//
// sync.force is gated like a read of the chat: a pull lands older history
// in the readable store, so it is limited to chats the allowlist grants
// ActionRead (a sweep skips the others).
func registerSyncMethods(d *app.Dispatcher, adapter *wmAdapter.Adapter, allow app.Allowlist) {
	d.RegisterMethod("sync.force", makeSyncForceHandler(adapter, allow))
	d.RegisterMethod("sync.status", makeSyncStatusHandler(adapter))
}

type syncForceParams struct {
	Chat  string `json:"chat"`
	Count int    `json:"count"`
	// OlderHistory explicitly asks for history before the newest stored row
	// when no post-recovery anchor exists; reachability stays false.
	OlderHistory bool `json:"older_history"`
	Async        bool `json:"async"` // read by the dispatch boundary (FR-116-16)
}

type syncForceResult struct {
	Schema    string `json:"schema"`
	Requested bool   `json:"requested"`
	Connected bool   `json:"connected"`
	Chat      string `json:"chat,omitempty"`
	Count     int    `json:"count"`
	Received  int    `json:"received"`
	Delivered bool   `json:"delivered"`
	TimedOut  bool   `json:"timedOut"`
	Async     bool   `json:"async"`
	// FR-116-25: present whenever a recovery gap is tracked. It never
	// claims missing messages were restored.
	GapReachable *bool                `json:"gap_reachable,omitempty"`
	Reason       string               `json:"reason,omitempty"`
	Budget       string               `json:"budget,omitempty"`
	Chats        []wmAdapter.ChatSync `json:"chats,omitempty"`
}

// syncForcer is the adapter's pull, narrowed so tests can slow it down.
type syncForcer interface {
	ForceSync(ctx context.Context, chat domain.JID, count int, olderHistory bool) (wmAdapter.ForceSyncResult, error)
}

// pullAllowed is sync.force's gate on one chat: the action the registry
// (app.methodActions) names for it.
func pullAllowed(allow app.Allowlist, c domain.JID) bool {
	action, _ := app.ActionForMethod("sync.force")
	return allow.Allows(c, action)
}

func readFilter(allow app.Allowlist) func(domain.JID) bool {
	return func(c domain.JID) bool { return pullAllowed(allow, c) }
}

func makeSyncForceHandler(adapter syncForcer, allow app.Allowlist) func(context.Context, json.RawMessage) (json.RawMessage, error) {
	return func(ctx context.Context, raw json.RawMessage) (json.RawMessage, error) {
		var p syncForceParams
		if len(raw) > 0 {
			if err := json.Unmarshal(raw, &p); err != nil {
				return nil, app.InvalidParams(err.Error())
			}
		}
		var chat domain.JID
		if p.Chat != "" {
			j, err := domain.Parse(p.Chat)
			if err != nil {
				return nil, app.ErrInvalidJID
			}
			chat = j
		}
		if !chat.IsZero() && !pullAllowed(allow, chat) {
			return nil, app.ErrNotAllowlisted
		}
		ctx = wmAdapter.WithChatFilter(ctx, readFilter(allow))
		r, err := adapter.ForceSync(ctx, chat, p.Count, p.OlderHistory)
		if err != nil {
			return nil, err
		}
		return json.Marshal(syncForceResult{
			Schema:    "wa.sync.force/v1",
			Requested: r.Requested,
			Connected: r.Connected,
			Chat:      r.Chat,
			Count:     r.Count,
			Received:  r.Received,
			Delivered: r.Delivered,
			TimedOut:  r.TimedOut,
			Async:     r.Async,

			GapReachable: r.GapReachable,
			Reason:       r.Reason,
			Budget:       r.Budget,
			Chats:        r.Chats,
		})
	}
}

type syncStatusResult struct {
	Schema            string `json:"schema"`
	Syncing           bool   `json:"syncing"`
	InFlightForceReqs int    `json:"inFlightForceReqs"`
	QueueDepth        int    `json:"queueDepth"`
	QueueCap          int    `json:"queueCap"`
}

func makeSyncStatusHandler(adapter *wmAdapter.Adapter) func(context.Context, json.RawMessage) (json.RawMessage, error) {
	return func(_ context.Context, _ json.RawMessage) (json.RawMessage, error) {
		s := adapter.SyncStatus()
		return json.Marshal(syncStatusResult{
			Schema:            "wa.sync.status/v1",
			Syncing:           s.Syncing,
			InFlightForceReqs: s.InFlightForceReqs,
			QueueDepth:        s.QueueDepth,
			QueueCap:          s.QueueCap,
		})
	}
}

// reacher is the adapter's verdict-only view, narrowed so tests can fake it.
type reacher interface {
	Reachability(ctx context.Context, chat domain.JID, older bool) wmAdapter.ForceSyncResult
}

// syncReach is sync.force's verdict without its pull, for a job answer.
func syncReach(adapter reacher, allow app.Allowlist) func(context.Context, json.RawMessage) map[string]any {
	return func(ctx context.Context, raw json.RawMessage) map[string]any {
		var p syncForceParams
		_ = json.Unmarshal(raw, &p) // the handler reports bad params
		out := map[string]any{}
		var chat domain.JID
		if p.Chat != "" {
			j, err := domain.Parse(p.Chat)
			if err != nil || !pullAllowed(allow, j) {
				return out // the job fails with the typed refusal; a verdict about another chat would mislead
			}
			chat = j
		}
		r := adapter.Reachability(wmAdapter.WithChatFilter(ctx, readFilter(allow)), chat, p.OlderHistory)
		if r.GapReachable != nil {
			out["gap_reachable"], out["reason"] = *r.GapReachable, r.Reason
		}
		if len(r.Chats) > 0 {
			out["chats"] = r.Chats
		}
		return out
	}
}
