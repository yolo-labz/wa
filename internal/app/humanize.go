package app

import (
	"context"
	"math/rand/v2"
	"time"

	"github.com/yolo-labz/wa/v2/internal/domain"
)

// Humanize delay model (roadmap 2.3). The canonical pre-send hygiene flow
// — typing presence, then a jittered human-scale pause — productized
// behind the `humanize` param so agent callers don't hand-roll it.
//
// Tuning constraints:
//   - The floor must clear the PresenceSender 1/s/chat budget with jitter
//     at its 0.75 minimum, or the trailing "paused" frame is silently
//     budget-dropped: 1.5 s × 0.75 = 1.125 s > 1 s.
//   - The cap stays under WhatsApp's ~10 s server-side composing
//     auto-expiry so the indicator never outlives the flow.
const (
	humanizeBaseDelay = 1500 * time.Millisecond
	humanizePerChar   = 35 * time.Millisecond
	humanizeMaxDelay  = 6 * time.Second
)

// defaultHumanizeDelay returns base + per-char typing time, capped, with
// ±25% jitter so repeated sends don't tick at a metronomic interval.
func defaultHumanizeDelay(bodyLen int) time.Duration {
	d := humanizeBaseDelay + time.Duration(bodyLen)*humanizePerChar
	if d > humanizeMaxDelay {
		d = humanizeMaxDelay
	}
	return time.Duration(float64(d) * (0.75 + rand.Float64()*0.5)) //nolint:gosec // jitter, not crypto
}

// humanizeDelay resolves the per-send delay: the package-private test seam
// when set, the production default otherwise.
func (d *Dispatcher) humanizeDelay(bodyLen int) time.Duration {
	if d.humanizeDelayFn != nil {
		return d.humanizeDelayFn(bodyLen)
	}
	return defaultHumanizeDelay(bodyLen)
}

// humanizeBeforeSend runs the pre-send hygiene flow: composing presence →
// jittered delay → paused presence. Runs AFTER the safety pipeline (the
// rate-limit token is consumed at request time, and a refused send must
// not leak a typing indicator to the target) and inside the send span so
// the delay is visible in traces.
//
// Presence is best-effort: when no PresenceSender is wired (or a frame
// fails) the flow degrades to delay-only rather than blocking the send —
// the delay is the load-bearing half. Context cancellation during the
// delay aborts the send with ctx.Err().
func (d *Dispatcher) humanizeBeforeSend(ctx context.Context, chat domain.JID, bodyLen int) error {
	delay := d.humanizeDelay(bodyLen)

	ps, hasPresence := d.presence()
	if hasPresence {
		if err := ps.SendComposing(ctx, chat, "composing", delay); err != nil {
			d.log.Debug("humanize: composing presence failed, continuing delay-only", "err", err)
		}
	}

	t := time.NewTimer(delay)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
	}

	if hasPresence {
		if err := ps.SendComposing(ctx, chat, "paused", 0); err != nil {
			d.log.Debug("humanize: paused presence failed", "err", err)
		}
	}
	return nil
}
