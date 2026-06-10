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

	// backfillTimeoutSec bounds one post-recover backfill attempt. Wider
	// than recoverTimeoutSec because the backfill first waits (bounded)
	// for the fresh login to settle before issuing the on-demand pull.
	backfillTimeoutSec = 60

	// backoffCapDefaultSec is the default ceiling on the reconnect
	// backoff gap (spec 110g backoff extension, PR #224). One hour bounds
	// the worst-case heal latency for a genuine zombie that starts during
	// a long quiet stretch: the next scheduled recover + backfill is at
	// most this far away, so messages are delayed, never lost.
	backoffCapDefaultSec = 3600

	// backoffCapMaxSec is the hard upper clamp on
	// WA_SOFT_STALE_BACKOFF_CAP_SEC. Beyond a day the periodic
	// reconnect+backfill stops being a useful liveness insurance.
	backoffCapMaxSec = 86400

	// backoffShiftMax caps the exponent in threshold<<streak so a long
	// quiet night (many consecutive recovers) cannot overflow int64. With
	// threshold <= 3600 and shift 16 the product already exceeds any
	// legal cap by orders of magnitude.
	backoffShiftMax = 16
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
	// FR-009), only on a healthy->stale edge — and, once an episode has
	// had its first edge-recover, re-fires while staleness persists at
	// exponentially backed-off gaps (backoff extension, PR #224).
	Recover func(context.Context) error
	// RecoverCooldownSec is the minimum gap between reconnects; <= 0 uses
	// recoverCooldownDefaultSec. Ignored when Recover is nil.
	RecoverCooldownSec int
	// BackoffCapSec is the ceiling on the reconnect backoff gap (backoff
	// extension, PR #224). Consecutive recovers with zero organic traffic
	// between them double the required gap from ThresholdSec up to this
	// cap; any message/receipt resets the gap to the plain cooldown.
	// <= 0 uses backoffCapDefaultSec. Setting it equal to ThresholdSec
	// reproduces the pre-backoff fixed-cadence behaviour. Ignored when
	// Recover is nil.
	BackoffCapSec int

	// Backfill, when non-nil, runs AFTER a successful recover reconnect to
	// pull the messages missed during the stall window (spec 110g backfill
	// extension, opt-in via WA_SOFT_STALE_BACKFILL). It only runs when
	// Recover is set and the reconnect succeeded — a backfill over the
	// still-zombie link would go into the void — and shares the recover
	// goroutine + cooldown gate. nil keeps recovery reconnect-only.
	// Best-effort: a failure leaves the gap but never destabilises the
	// daemon.
	Backfill func(context.Context) error
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
	var rec recoverState
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
		prev := current
		current = softStaleStep(deps, current, &seq)
		if deps.Recover == nil || current != softStaleStateStale {
			continue
		}
		// Recover extension (spec 110g, opt-in): a genuine healthy->stale
		// edge means whatsmeow believes the link is up but no traffic has
		// flowed past the threshold — the zombie signature. Force a
		// reconnect. The edge gate matches softStaleEmit's, so recovery
		// fires exactly when a softStale event is emitted (never on the
		// silent unknown->stale boot classification).
		//
		// Backoff extension (PR #224): while staleness persists after the
		// episode's first edge-recover, KEEP re-firing at exponentially
		// backed-off gaps instead of stopping. The periodic
		// reconnect+backfill is the liveness insurance that heals a
		// recurrent zombie during a long quiet stretch; the backoff is
		// what keeps a merely-quiet account (overnight: zero organic
		// traffic, every 16min a pointless reconnect) from churning the
		// link ~90 times a night.
		switch prev {
		case softStaleStateHealthy:
			maybeRecover(ctx, deps, &rec, true)
		case softStaleStateStale:
			maybeRecover(ctx, deps, &rec, false)
		}
	}
}

// recoverState is the recovery bookkeeping owned by the watchdog
// goroutine (no synchronisation needed — single writer).
type recoverState struct {
	// lastRecoverUnix is when the last recover was ISSUED (not completed),
	// so a failed reconnect does not immediately retry.
	lastRecoverUnix int64
	// quietStreak counts consecutive recovers with zero organic traffic
	// (bridge.LastTrafficUnix) between them. Drives the backoff gap.
	quietStreak int
}

// recoverGapSec returns the minimum gap required before the next recover
// given the current quiet streak: the plain cooldown for streak 0, then
// threshold×2^(streak-1) capped at BackoffCapSec. The cap never drops
// below the threshold — a sub-threshold cap could never be reached by
// the state machine anyway (staleness itself takes one threshold).
func recoverGapSec(deps SoftStaleDeps, streak int) int64 {
	cooldown := deps.RecoverCooldownSec
	if cooldown <= 0 {
		cooldown = recoverCooldownDefaultSec
	}
	gap := int64(cooldown)
	if streak < 1 {
		return gap
	}
	shift := streak - 1
	if shift > backoffShiftMax {
		shift = backoffShiftMax
	}
	if b := int64(deps.ThresholdSec) << shift; b > gap {
		gap = b
	}
	capSec := int64(deps.BackoffCapSec)
	if capSec <= 0 {
		capSec = backoffCapDefaultSec
	}
	if capSec < int64(deps.ThresholdSec) {
		capSec = int64(deps.ThresholdSec)
	}
	if gap > capSec {
		gap = capSec
	}
	return gap
}

// maybeRecover invokes deps.Recover on its own goroutine, rate-limited by
// the backoff gap (recoverGapSec). edge marks a genuine healthy->stale
// transition; re-fire ticks (staleness persisting) pass edge=false and
// are inert until the episode's first edge-recover has happened — this
// preserves the FR-009 rule that the silent unknown->stale boot
// classification never triggers recovery. Suppressions are logged only
// on edges so the per-tick re-fire checks don't flood the log. Called
// only from the watchdog goroutine, so recoverState needs no
// synchronisation.
func maybeRecover(ctx context.Context, deps SoftStaleDeps, st *recoverState, edge bool) {
	if !edge && st.lastRecoverUnix == 0 {
		return
	}
	// Organic traffic since the last forced reconnect proves the link
	// carries real events again — reset the streak so detection latency
	// snaps back to one threshold.
	if st.lastRecoverUnix != 0 && deps.Bridge.LastTrafficUnix() > st.lastRecoverUnix {
		st.quietStreak = 0
	}
	now := deps.NowFn().Unix()
	gap := recoverGapSec(deps, st.quietStreak)
	if st.lastRecoverUnix != 0 && now-st.lastRecoverUnix < gap {
		if edge && deps.Log != nil {
			deps.Log.Info("soft-stale recover suppressed by backoff",
				"sinceLastSec", now-st.lastRecoverUnix, "requiredGapSec", gap,
				"quietStreak", st.quietStreak, "profile", deps.Profile)
		}
		return
	}
	st.lastRecoverUnix = now
	st.quietStreak++
	if deps.Log != nil {
		deps.Log.Warn("soft-stale recover: forcing reconnect",
			"quietStreak", st.quietStreak,
			"nextGapSec", recoverGapSec(deps, st.quietStreak),
			"profile", deps.Profile)
	}
	spawnRecoverAndBackfill(ctx, deps)
}

// spawnRecoverAndBackfill runs the reconnect (and the opt-in backfill
// that follows a successful one) on its own goroutine so a slow
// handshake never stalls the watchdog's detection loop.
func spawnRecoverAndBackfill(ctx context.Context, deps SoftStaleDeps) {
	recoverFn := deps.Recover
	backfillFn := deps.Backfill
	profile := deps.Profile
	log := deps.Log
	go func() {
		// Last-resort guard: this goroutine has no dispatcher panic recovery
		// above it, so an unrecovered panic in recover/backfill would take the
		// whole daemon down. The adapter's history path recovers its own
		// panics (PR #222); this is defence in depth. PR #222.
		defer func() {
			if r := recover(); r != nil && log != nil {
				log.Error("soft-stale recover/backfill goroutine panicked (recovered)",
					"panic", r, "profile", profile)
			}
		}()
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
		// Backfill extension (opt-in): the reconnect re-opened the socket,
		// but WhatsApp already "delivered" the stall-window messages into
		// the dead socket and will not redeliver them. Pull them back with
		// an explicit on-demand history request. Best-effort, own timeout —
		// a failure leaves the gap but never destabilises the daemon.
		if backfillFn == nil {
			return
		}
		bctx, bcancel := context.WithTimeout(ctx, backfillTimeoutSec*time.Second)
		defer bcancel()
		if err := backfillFn(bctx); err != nil {
			if log != nil {
				log.Warn("soft-stale backfill: pull failed", "error", err, "profile", profile)
			}
			return
		}
		if log != nil {
			log.Info("soft-stale backfill: pull requested", "profile", profile)
		}
	}()
}

// parseSoftStaleBool recognises the truthy token set shared by the
// soft-stale opt-in flags: "1", "true", "yes", "on" (case-insensitive,
// trimmed). Everything else — including unset/empty/garbage — is false.
func parseSoftStaleBool(raw string) bool {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
}

// ParseSoftStaleRecover reports whether the opt-in soft-stale recovery
// action is enabled (spec 110g recover extension). Everything other than a
// truthy token — including unset/empty — leaves the watchdog detect-only,
// the spec-110g default.
func ParseSoftStaleRecover(raw string) bool { return parseSoftStaleBool(raw) }

// ParseSoftStaleBackfill reports whether the opt-in post-recover backfill
// is enabled (spec 110g backfill extension). Same truthy token set as
// ParseSoftStaleRecover. It is effective only alongside recovery — a
// backfill over a still-zombie link is pointless — and main() enforces
// that pairing (and warns when backfill is set without recover).
func ParseSoftStaleBackfill(raw string) bool { return parseSoftStaleBool(raw) }

// ParseSoftStaleBackoffCapSec reads WA_SOFT_STALE_BACKOFF_CAP_SEC and
// returns the effective backoff ceiling (backoff extension, PR #224).
// Unset/empty/zero/invalid all fall back to backoffCapDefaultSec —
// the backoff is part of recovery's contract, not separately opt-in.
// Values below the threshold are clamped UP to it (a sub-threshold cap
// is unreachable; threshold == cap reproduces the pre-backoff fixed
// cadence) and values above backoffCapMaxSec are clamped DOWN, both
// with a warning so operators see what the daemon actually used.
func ParseSoftStaleBackoffCapSec(raw string, thresholdSec int, log *slog.Logger) int {
	capSec := backoffCapDefaultSec
	if raw != "" {
		n, err := strconv.Atoi(raw)
		switch {
		case err != nil:
			if log != nil {
				log.Warn("WA_SOFT_STALE_BACKOFF_CAP_SEC: not an integer, using default",
					"value", raw, "default", backoffCapDefaultSec, "error", err)
			}
		case n <= 0:
			if log != nil {
				log.Warn("WA_SOFT_STALE_BACKOFF_CAP_SEC: non-positive, using default",
					"requested", n, "default", backoffCapDefaultSec)
			}
		case n > backoffCapMaxSec:
			if log != nil {
				log.Warn("WA_SOFT_STALE_BACKOFF_CAP_SEC: clamped DOWN to maximum",
					"requested", n, "effective", backoffCapMaxSec)
			}
			capSec = backoffCapMaxSec
		default:
			capSec = n
		}
	}
	if capSec < thresholdSec {
		if log != nil {
			log.Warn("WA_SOFT_STALE_BACKOFF_CAP_SEC: clamped UP to threshold",
				"requested", capSec, "effective", thresholdSec)
		}
		capSec = thresholdSec
	}
	return capSec
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
