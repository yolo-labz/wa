package main

import (
	"context"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"time"

	"github.com/yolo-labz/wa/v2/internal/app"
	"github.com/yolo-labz/wa/v2/internal/domain"
)

// Spec 110g — soft-stale watchdog.
//
// Rationale: whatsmeow's keepalive considers a websocket "alive" as long
// as PINGs round-trip every 20–30s. In practice the daemon can spend
// hours in a state where keepalive succeeds but no inbound traffic ever
// arrives — the WhatsApp side silently demoted the device but the server
// kept answering pings. Operators observed this on 19/05/2026 with
// wa-burocracy: `wa health` reported paired:true connected:true while
// the phone showed the device offline. There was no signal in the daemon.
//
// The watchdog closes that observability gap. Detect-and-emit only —
// it does NOT call Logout, does NOT trigger reconnect, does NOT mutate
// session state. False positives are silent and cheap; false negatives
// are what we are trying to fix.
//
// Threshold semantics (FR-007):
//
//   - thresholdSec < 0  : clamped to 0 (disabled).
//   - thresholdSec == 0 : disabled. The goroutine returns immediately.
//   - 0 < thresholdSec < softStaleMinSec : clamped UP to softStaleMinSec.
//   - thresholdSec > softStaleMaxSec     : clamped DOWN to softStaleMaxSec.
//
// The clamp lives in ParseSoftStaleThresholdSec so the dispatcher and
// the watchdog see the same number.

const (
	// softStaleMinSec is the lower bound on the soft-stale threshold.
	// Below 30s the false-positive rate from a quiet chat dominates the
	// signal — WhatsApp routinely sends no inbound traffic for a minute
	// at a time on a healthy link.
	softStaleMinSec = 30

	// softStaleMaxSec is the upper bound on the soft-stale threshold.
	// An hour is the longest credible silence an operator should accept
	// without investigating; beyond that the gap is no longer useful
	// telemetry.
	softStaleMaxSec = 3600

	// softStaleTickCapSec caps the tick period at 60s. Below that the
	// goroutine is responsive enough to surface a transition within one
	// threshold-third of the actual stall; above that it would risk
	// emitting softStale several ticks late on a long threshold.
	softStaleTickCapSec = 60

	// recoverCooldownDefaultSec is the minimum gap between two reconnects
	// when SoftStaleDeps.RecoverCooldownSec is unset (spec 110g recover
	// extension). The healthy->stale transition gate already limits firing
	// to ~once per stale episode; this cooldown only guards against
	// threshold-boundary flapping.
	recoverCooldownDefaultSec = 300

	// recoverTimeoutSec bounds a single reconnect attempt so a hung
	// Connect cannot leak the recovery goroutine.
	recoverTimeoutSec = 30
)

// ParseSoftStaleThresholdSec reads WA_SOFT_STALE_THRESHOLD_SEC and
// returns the effective threshold per spec 110g FR-007. Returns 0 when
// the env var is unset, empty, or literally "0" — those three cases all
// disable the watchdog. Invalid integers (non-numeric, negative) are
// logged and treated as disabled. Values inside the legal range pass
// through; out-of-range values are clamped with a warning so operators
// see what the daemon actually used.
func ParseSoftStaleThresholdSec(raw string, log *slog.Logger) int {
	if raw == "" {
		return 0
	}
	n, err := strconv.Atoi(raw)
	if err != nil {
		if log != nil {
			log.Warn("WA_SOFT_STALE_THRESHOLD_SEC: not an integer, watchdog disabled",
				"value", raw, "error", err)
		}
		return 0
	}
	if n <= 0 {
		return 0
	}
	if n < softStaleMinSec {
		if log != nil {
			log.Warn("WA_SOFT_STALE_THRESHOLD_SEC: clamped UP to minimum",
				"requested", n, "effective", softStaleMinSec)
		}
		return softStaleMinSec
	}
	if n > softStaleMaxSec {
		if log != nil {
			log.Warn("WA_SOFT_STALE_THRESHOLD_SEC: clamped DOWN to maximum",
				"requested", n, "effective", softStaleMaxSec)
		}
		return softStaleMaxSec
	}
	return n
}

// softStaleTickFor returns the tick period for a given threshold:
// min(threshold/3, softStaleTickCapSec). The /3 keeps the worst-case
// detection latency under one threshold; the cap keeps the goroutine
// from busy-spinning on small thresholds.
func softStaleTickFor(thresholdSec int) time.Duration {
	t := thresholdSec / 3
	if t < 1 {
		t = 1
	}
	if t > softStaleTickCapSec {
		t = softStaleTickCapSec
	}
	return time.Duration(t) * time.Second
}

// SoftStaleDeps is the dependency surface for runSoftStaleWatchdog.
// Carrying these as a struct (instead of bare arguments) keeps the call
// site in main() short and makes the unit test rig trivial — the test
// substitutes nowFn for synctest-controlled time and probe for a
// controllable bool. Audit is intentionally absent: the synthetic
// ConnectivityHealthEvent is itself the durable signal (ring-buffer
// subscribers see it), and double-recording would violate CLAUDE.md
// rule 12 (no silent fallbacks, no parallel truth sources).
type SoftStaleDeps struct {
	Bridge       *app.EventBridge
	Probe        app.WebsocketProbe
	Profile      string
	ThresholdSec int
	NowFn        func() time.Time
	Log          *slog.Logger

	// Recover, when non-nil, is invoked on a genuine healthy->stale
	// transition to force a websocket reconnect (spec 110g recover
	// extension, opt-in via WA_SOFT_STALE_RECOVER). It runs on its own
	// goroutine so a slow handshake never stalls detection. nil keeps the
	// watchdog detect-only — the spec-110g default. Recovery never fires
	// on the unknown->stale boot classification (which is silent per
	// FR-009), only on a healthy->stale edge.
	Recover func(context.Context) error
	// RecoverCooldownSec is the minimum gap between reconnects; <= 0 uses
	// recoverCooldownDefaultSec. Ignored when Recover is nil.
	RecoverCooldownSec int
}

// runSoftStaleWatchdog is the long-running goroutine that polls the
// bridge's last-event timestamp and the websocket probe, then emits
// synthetic ConnectivityHealthEvent transitions when the (state, stale)
// pair flips.
//
// The state machine is deliberately tiny — two states, debounced — so
// operators don't have to reason about edge ordering. Spec 110g FR-008.
//
//	stateHealthy : websocket up AND staleSeconds < threshold
//	stateStale   : websocket up AND staleSeconds >= threshold
//
// Transitions emit ConnectivityHealthEvent{softStale} or {restored}.
// Hard websocket-down is NOT a watchdog concern; whatsmeow's own
// reconnect loop owns that path and the adapter already surfaces
// state.disconnected.
// Soft-stale state machine values. Module-level so the helper functions
// below share the same enum without re-declaring it.
const (
	softStaleStateHealthy = 0
	softStaleStateStale   = 1
	softStaleStateUnknown = 2 // initial; suppresses the first transition emit
)

func runSoftStaleWatchdog(ctx context.Context, deps SoftStaleDeps) {
	if !softStaleArm(&deps) {
		return
	}
	tick := time.NewTicker(softStaleTickFor(deps.ThresholdSec))
	defer tick.Stop()

	current := softStaleStateUnknown
	var seq uint64
	var lastRecoverUnix int64
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
		prev := current
		current = softStaleStep(deps, current, &seq)
		// Recover extension (spec 110g, opt-in): a genuine healthy->stale
		// edge means whatsmeow believes the link is up but no traffic has
		// flowed past the threshold — the zombie signature. Force a
		// reconnect. The edge gate matches softStaleEmit's, so recovery
		// fires exactly when a softStale event is emitted (never on the
		// silent unknown->stale boot classification).
		if deps.Recover != nil && prev == softStaleStateHealthy && current == softStaleStateStale {
			maybeRecover(ctx, deps, &lastRecoverUnix)
		}
	}
}

// maybeRecover invokes deps.Recover on its own goroutine, rate-limited by
// the cooldown. The cooldown counter advances on issue (not completion),
// so a failed reconnect does not immediately retry — it waits for the
// next healthy->stale edge or whatsmeow's own reconnect loop. Called only
// from the watchdog goroutine, so lastRecoverUnix needs no synchronisation.
func maybeRecover(ctx context.Context, deps SoftStaleDeps, lastRecoverUnix *int64) {
	cooldown := deps.RecoverCooldownSec
	if cooldown <= 0 {
		cooldown = recoverCooldownDefaultSec
	}
	now := deps.NowFn().Unix()
	if *lastRecoverUnix != 0 && now-*lastRecoverUnix < int64(cooldown) {
		if deps.Log != nil {
			deps.Log.Info("soft-stale recover suppressed by cooldown",
				"sinceLastSec", now-*lastRecoverUnix, "cooldownSec", cooldown, "profile", deps.Profile)
		}
		return
	}
	*lastRecoverUnix = now
	if deps.Log != nil {
		deps.Log.Warn("soft-stale recover: forcing reconnect", "profile", deps.Profile)
	}
	recoverFn := deps.Recover
	profile := deps.Profile
	log := deps.Log
	go func() {
		rctx, cancel := context.WithTimeout(ctx, recoverTimeoutSec*time.Second)
		defer cancel()
		if err := recoverFn(rctx); err != nil {
			if log != nil {
				log.Warn("soft-stale recover: reconnect failed", "error", err, "profile", profile)
			}
			return
		}
		if log != nil {
			log.Info("soft-stale recover: reconnect issued", "profile", profile)
		}
	}()
}

// ParseSoftStaleRecover reports whether the opt-in soft-stale recovery
// action is enabled (spec 110g recover extension). Recognises "1",
// "true", "yes", "on" (case-insensitive, trimmed); everything else —
// including unset/empty — is false, leaving the watchdog detect-only,
// the spec-110g default.
func ParseSoftStaleRecover(raw string) bool {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
}

// softStaleArm validates dependencies, defaults NowFn, and logs the
// banner line. Returns false if the watchdog must stay inert.
func softStaleArm(deps *SoftStaleDeps) bool {
	if deps.ThresholdSec <= 0 {
		if deps.Log != nil {
			deps.Log.Info("soft-stale watchdog disabled (threshold <= 0)")
		}
		return false
	}
	if deps.Bridge == nil || deps.Probe == nil {
		if deps.Log != nil {
			deps.Log.Warn("soft-stale watchdog: missing bridge or probe, disabled")
		}
		return false
	}
	if deps.NowFn == nil {
		deps.NowFn = time.Now
	}
	if deps.Log != nil {
		deps.Log.Info("soft-stale watchdog active",
			"thresholdSec", deps.ThresholdSec,
			"tick", softStaleTickFor(deps.ThresholdSec))
	}
	return true
}

// softStaleStep evaluates one tick of the state machine and returns the
// next state. Emits a synthetic ConnectivityHealthEvent on a genuine
// transition; the unknown→X first classification is recorded silently
// (FR-009) to avoid a spurious "restored" on boot.
func softStaleStep(deps SoftStaleDeps, current int, seq *uint64) int {
	lastEvent := deps.Bridge.LastEventUnix()
	if lastEvent == 0 {
		// Pre-pair / just-started — stay inert until first event lands.
		return current
	}
	if !deps.Probe.WebsocketConnected() {
		// Hard disconnect — whatsmeow owns recovery. Re-arm so the
		// first post-reconnect tick does not emit a spurious restored.
		return softStaleStateUnknown
	}
	stale := deps.NowFn().Unix() - lastEvent
	next := softStaleStateHealthy
	if stale >= int64(deps.ThresholdSec) {
		next = softStaleStateStale
	}
	if next == current {
		return current
	}
	if current == softStaleStateUnknown {
		// First real classification — record silently.
		return next
	}
	*seq++
	softStaleEmit(deps, next, stale, *seq)
	return next
}

// softStaleEmit pushes a synthetic ConnectivityHealthEvent for the
// transition and logs it. Detail string is identical for softStale and
// restored — operators read State to distinguish, Detail for the
// numbers behind the decision.
func softStaleEmit(deps SoftStaleDeps, next int, stale int64, seq uint64) {
	state := domain.HealthRestored
	if next == softStaleStateStale {
		state = domain.HealthSoftStale
	}
	detail := fmt.Sprintf("staleSeconds=%d thresholdSec=%d", stale, deps.ThresholdSec)
	deps.Bridge.EmitSynthetic(domain.ConnectivityHealthEvent{
		ID:     domain.EventID("softstale-" + strconv.FormatUint(seq, 10)),
		TS:     deps.NowFn(),
		State:  state,
		Detail: detail,
	})
	if deps.Log != nil {
		deps.Log.Info("soft-stale watchdog transition",
			"state", state.String(),
			"staleSeconds", stale,
			"thresholdSec", deps.ThresholdSec,
			"profile", deps.Profile)
	}
}
