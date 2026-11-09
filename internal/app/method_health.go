package app

import (
	"context"
	"encoding/json"
	"time"

	"github.com/yolo-labz/wa/v2/internal/domain"
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
	domain.StorageHealth
	Health       string   `json:"health,omitempty"`
	Reasons      []string `json:"reasons,omitempty"`
	unready      []string
	account      string
	Schema       string `json:"schema,omitempty"`
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
	// Degraded lists optional subsystems lost at startup (best-effort
	// store open failures: "contacts", "events", "webhooks"). Omitted
	// when fully provisioned. SF-03 — additive omitempty field, same
	// no-v2-bump rule as the 110g fields above.
	Degraded []string `json:"degraded,omitempty"`
	// StreamErrors counts upstream event-stream read failures since
	// boot (SF-08). Nonzero with a recent LastStreamErrorTs means the
	// subscribe pipeline is degraded even though the daemon is up.
	// Additive omitempty fields, same no-v2-bump rule as above.
	StreamErrors int64 `json:"streamErrors,omitempty"`
	// LastStreamErrorTs is the unix-time of the most recent stream
	// read failure. Omitted when no failure has occurred.
	LastStreamErrorTs int64 `json:"lastStreamErrorTs,omitempty"`
}

// handleHealth implements the "health" JSON-RPC method (FR-040,
// extended by spec 110g). It is non-blocking: every value it reads is
// either an in-memory atomic or a cached session row. It must return
// in well under 1s per SC-008.
func (d *Dispatcher) handleHealth(ctx context.Context, _ json.RawMessage) (json.RawMessage, error) {
	return marshalResult(d.healthSnapshot(ctx))
}

func (d *Dispatcher) healthSnapshot(ctx context.Context) healthResult {
	lastEventTs := d.bridge.LastEventUnix()
	res := healthResult{
		Schema:                "wa.health/v1",
		Profile:               d.profile,
		LastEventTs:           lastEventTs,
		SoftStaleThresholdSec: d.softStaleSec,
		Degraded:              d.degraded,
		StreamErrors:          d.bridge.StreamErrors(),
		LastStreamErrorTs:     d.bridge.LastStreamErrorUnix(),
	}

	var sess domain.Session
	if d.session != nil {
		if loaded, err := d.session.Load(ctx); err == nil {
			sess = loaded
		}
	}
	paired := !sess.IsZero()
	if paired {
		res.Paired = true
		user := sess.JID().User()
		res.account = "***" + user[max(0, len(user)-4):]
		if ts := sess.CreatedAt(); !ts.IsZero() {
			res.SessionSince = ts.Unix()
		}
	}

	// No live probe is not evidence of connectivity (spec 116 F5).
	res.Connected = paired && d.websocket != nil && d.websocket.WebsocketConnected()

	if d.storageHealth != nil {
		res.StorageHealth = d.storageHealth()
		lastEventTs = res.LastReceivedUnixS
		res.LastEventTs = lastEventTs
	}

	// Spec 110g FR-005: StaleSeconds + StaleState. Computed only when
	// the daemon has actually observed at least one event AND a probe
	// is wired — otherwise the values are meaningless and omitted.
	if lastEventTs > 0 && d.websocket != nil {
		stale := max(time.Now().Unix()-lastEventTs, 0)
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

	staleSec, minFree := d.ingestStaleSec, d.minFreeBytes
	if staleSec <= 0 {
		staleSec = 300
	}
	if minFree == 0 {
		minFree = 64 * 1024 * 1024
	}
	res.Health, res.Reasons, res.unready = healthDecision([7]bool{
		!paired, res.Storage == "unknown", res.Storage == "failed",
		paired && !res.Connected, res.Connected && res.IngestLagS > staleSec,
		res.StaleState == staleStateStale, res.Storage == "ok" && res.FreeBytes < minFree,
	})
	return res
}

// healthDecision is the single severity/readiness table; warnings do not drain.
func healthDecision(holds [7]bool) (health string, reasons, unready []string) {
	health = "healthy"
	for i, reason := range []string{"not_paired", "storage_unknown", "storage_unwritable", "disconnected", "ingest_stale", "soft_stale", "storage_low"} {
		if !holds[i] {
			continue
		}
		reasons = append(reasons, reason)
		if health == "healthy" {
			health = "degraded"
		}
		if i < 3 {
			health = "down"
		}
		if i < 5 {
			unready = append(unready, reason)
		}
	}
	return
}

// ReadinessReasons reads cached state only. Each returned token is a 503 reason.
func (d *Dispatcher) ReadinessReasons(ctx context.Context) []string {
	if d == nil || d.session == nil {
		return []string{"not_paired"}
	}
	return d.healthSnapshot(ctx).unready
}

func (d *Dispatcher) IsReady(ctx context.Context) bool {
	return len(d.ReadinessReasons(ctx)) == 0
}
