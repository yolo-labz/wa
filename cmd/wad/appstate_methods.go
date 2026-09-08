package main

import (
	"context"
	"encoding/json"
	"fmt"

	wmAdapter "github.com/yolo-labz/wa/v2/internal/adapters/secondary/whatsmeow"
	"github.com/yolo-labz/wa/v2/internal/app"
)

// registerAppStateMethods registers appstate.resync — the recovery path
// for a diverged app-state snapshot.
//
// Every app-state write (chat.archive / mute / pin / markUnread,
// labels.*, message.revoke scope=self) sends a patch keyed on the LOCAL
// version. When that stops matching the server's, the server answers 409
// and whatsmeow cannot verify the catch-up patches ("mismatching
// LTHash"). All of those methods then fail permanently. Before this the
// only way out was re-pairing — a one-way door for a daemon whose whole
// value is a warm session.
func registerAppStateMethods(d *app.Dispatcher, adapter *wmAdapter.Adapter) {
	d.RegisterMethod("appstate.resync", makeAppStateResyncHandler(adapter))
}

type appStateResyncParams struct {
	// Collection is a whatsmeow patch name ("regular_high", "regular_low",
	// "regular", "critical_block", "critical_unblock_low"). Empty means
	// every collection.
	Collection string `json:"collection,omitempty"`
	// Full discards the local snapshot and rebuilds. Default true: a
	// catch-up cannot repair a hash mismatch, and repair is the reason
	// this method exists. Pass false for the cheap incremental pull.
	Full *bool `json:"full,omitempty"`
}

func makeAppStateResyncHandler(adapter *wmAdapter.Adapter) func(context.Context, json.RawMessage) (json.RawMessage, error) {
	return func(ctx context.Context, raw json.RawMessage) (json.RawMessage, error) {
		var p appStateResyncParams
		if len(raw) > 0 {
			if err := json.Unmarshal(raw, &p); err != nil {
				return nil, app.InvalidParams("appstate.resync: " + err.Error())
			}
		}
		full := true
		if p.Full != nil {
			full = *p.Full
		}

		targets := wmAdapter.AppStateCollections()
		if p.Collection != "" {
			targets = []string{p.Collection}
		}

		// Resync every requested collection, reporting per-collection
		// outcomes rather than aborting on the first failure: repairing
		// four of five is strictly better than repairing none, and the
		// caller needs to know WHICH one is still broken.
		type result struct {
			Collection string `json:"collection"`
			OK         bool   `json:"ok"`
			Error      string `json:"error,omitempty"`
		}
		out := make([]result, 0, len(targets))
		var firstErr error
		for _, c := range targets {
			if err := adapter.ResyncAppState(ctx, c, full); err != nil {
				out = append(out, result{Collection: c, OK: false, Error: err.Error()})
				if firstErr == nil {
					firstErr = err
				}
				continue
			}
			out = append(out, result{Collection: c, OK: true})
		}
		if firstErr != nil && len(targets) == 1 {
			return nil, fmt.Errorf("appstate.resync: %w", firstErr)
		}
		return json.Marshal(struct {
			Full    bool     `json:"full"`
			Results []result `json:"results"`
		}{full, out})
	}
}
