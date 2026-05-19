package app

import (
	"context"
	"encoding/json"
	"time"
)

// Spec 110g — soft-stale state vocabulary. Wire-stable strings surfaced
// under healthResult.StaleState. The three values map onto the watchdog's
// three observable conditions:
//
//   - "healthy" : websocket up AND staleSeconds < threshold.
//   - "stale"   : websocket up AND staleSeconds >= threshold. Operators
//     should investigate; whatsmeow's own keepalive thinks the link is
//     fine but no traffic has flowed.
//   - "unknown" : the daemon has no probe wired or threshold disabled
//     (WA_SOFT_STALE_THRESHOLD_SEC=0). Health is conservative — better to
//     declare unknown than synthesise a healthy lie.
const (
	staleStateHealthy = "healthy"
	staleStateStale   = "stale"
	staleStateUnknown = "unknown"
)

// healthResult is the JSON-RPC result for "health" (FR-040, extended by
// spec 110g). Shape is deliberately small and free of any adapter-owned
// fields — the CLI uses it as a <1s liveness probe, not as a status
// dashboard. Spec 110g adds three optional fields (omitempty preserves
// the wa.health/v1 schema additively per CLAUDE.md "no v2 bump" rule).
type healthResult struct {
	Schema       string `json:"schema"`
	Paired       bool   `json:"paired"`
	Connected    bool   `json:"connected"`
	Profile      string `json:"profile"`
	LastEventTs  int64  `json:"lastEventTs,omitempty"`
	SessionSince int64  `json:"sessionSince,omitempty"`
	// StaleSeconds is the wall-clock gap between now and the last inbound
	// event observed by the bridge. Zero when LastEventTs is zero (the
	// daemon has seen no events yet) — callers should ignore the field
	// in that case.
	StaleSeconds int64 `json:"staleSeconds,omitempty"`
	// StaleState collapses (websocket × staleSeconds × threshold) into a
	// single wire string operators can grep. See the const block above.
	StaleState string `json:"staleState,omitempty"`
	// SoftStaleThresholdSec echoes the configured threshold so clients
	// can verify the daemon honours their env override. Omitted when
	// the watchdog is disabled (threshold == 0).
	SoftStaleThresholdSec int `json:"softStaleThresholdSec,omitempty"`
}

// handleHealth implements the "health" JSON-RPC method (FR-040,
// extended by spec 110g). It is non-blocking: every value it reads is
// either an in-memory atomic or a cached session row. It must return
// in well under 1s per SC-008.
func (d *Dispatcher) handleHealth(ctx context.Context, _ json.RawMessage) (json.RawMessage, error) {
	lastEventTs := d.bridge.LastEventUnix()
	res := healthResult{
		Schema:                "wa.health/v1",
		Profile:               d.profile,
		LastEventTs:           lastEventTs,
		SoftStaleThresholdSec: d.softStaleSec,
	}

	sess, err := d.session.Load(ctx)
	paired := err == nil && !sess.IsZero()
	if paired {
		res.Paired = true
		if ts := sess.CreatedAt(); !ts.IsZero() {
			res.SessionSince = ts.Unix()
		}
	}

	// Spec 110g FR-004: Connected reflects the live websocket when the
	// WebsocketProbe port is wired. Pre-110g composition roots without
	// the probe fall back to the "session exists" approximation so the
	// pre-existing health behaviour is preserved.
	if d.websocket != nil {
		res.Connected = d.websocket.WebsocketConnected()
	} else {
		res.Connected = paired
	}

	// Spec 110g FR-005: StaleSeconds + StaleState. Computed only when
	// the daemon has actually observed at least one event AND a probe
	// is wired — otherwise the values are meaningless and omitted.
	if lastEventTs > 0 && d.websocket != nil {
		stale := time.Now().Unix() - lastEventTs
		if stale < 0 {
			stale = 0
		}
		res.StaleSeconds = stale
		switch {
		case d.softStaleSec <= 0:
			res.StaleState = staleStateUnknown
		case !res.Connected:
			res.StaleState = staleStateUnknown
		case stale >= int64(d.softStaleSec):
			res.StaleState = staleStateStale
		default:
			res.StaleState = staleStateHealthy
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
