package main

import (
	"context"
	"encoding/json"
	"fmt"

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
func registerSyncMethods(d *app.Dispatcher, adapter *wmAdapter.Adapter) {
	d.RegisterMethod("sync.force", makeSyncForceHandler(adapter))
	d.RegisterMethod("sync.status", makeSyncStatusHandler(adapter))
}

type syncForceParams struct {
	Chat  string `json:"chat"`
	Count int    `json:"count"`
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
}

func makeSyncForceHandler(adapter *wmAdapter.Adapter) func(context.Context, json.RawMessage) (json.RawMessage, error) {
	return func(ctx context.Context, raw json.RawMessage) (json.RawMessage, error) {
		var p syncForceParams
		if len(raw) > 0 {
			if err := json.Unmarshal(raw, &p); err != nil {
				return nil, fmt.Errorf("invalid params: %w", err)
			}
		}
		var chat domain.JID
		if p.Chat != "" {
			j, err := domain.Parse(p.Chat)
			if err != nil {
				return nil, fmt.Errorf("invalid chat %q: %w", p.Chat, err)
			}
			chat = j
		}
		r, err := adapter.ForceHistorySync(ctx, chat, p.Count)
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
