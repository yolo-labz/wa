package app

import (
	"context"
	"encoding/json"
)

// healthResult is the JSON-RPC result for "health" (FR-040). Shape is
// deliberately small and free of any adapter-owned fields — the CLI
// uses it as a <1s liveness probe, not as a status dashboard.
type healthResult struct {
	Schema       string `json:"schema"`
	Paired       bool   `json:"paired"`
	Connected    bool   `json:"connected"`
	Profile      string `json:"profile"`
	LastEventTs  int64  `json:"lastEventTs,omitempty"`
	SessionSince int64  `json:"sessionSince,omitempty"`
}

// handleHealth implements the "health" JSON-RPC method (FR-040). It is
// non-blocking: every value it reads is either an in-memory atomic or
// a cached session row. It must return in well under 1s per SC-008.
func (d *Dispatcher) handleHealth(ctx context.Context, _ json.RawMessage) (json.RawMessage, error) {
	res := healthResult{
		Schema:      "wa.health/v1",
		Profile:     d.profile,
		LastEventTs: d.bridge.LastEventUnix(),
	}

	sess, err := d.session.Load(ctx)
	if err == nil && !sess.IsZero() {
		res.Paired = true
		// Connected is currently approximated from "session exists"
		// because whatsmeow state is not surfaced through a port. The
		// Adapter-owned `state.connected` event updates LastEventTs on
		// transitions so operators still see the live edge.
		res.Connected = true
		if ts := sess.CreatedAt(); !ts.IsZero() {
			res.SessionSince = ts.Unix()
		}
	}
	return marshalResult(res)
}

// IsReady reports whether the dispatcher is paired AND notionally
// connected. Used by the HTTP /readyz probe (spec 109) so Dokku /
// k8s readiness probes can drain traffic when the daemon is in a
// pairing or reconnect state. The check is the same as handleHealth
// but skips JSON serialisation. Returns false on any session load
// error (treats unknown as not-ready, matches "fail closed" rule).
func (d *Dispatcher) IsReady(ctx context.Context) bool {
	if d == nil || d.session == nil {
		return false
	}
	sess, err := d.session.Load(ctx)
	if err != nil {
		return false
	}
	return !sess.IsZero()
}
