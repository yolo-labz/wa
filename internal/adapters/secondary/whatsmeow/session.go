package whatsmeow

import (
	"context"
	"time"

	"github.com/yolo-labz/wa/v2/internal/domain"
)

// Load implements app.SessionStore. It returns the live session derived
// from the whatsmeow client's Store.ID (the real pairing state lives
// inside whatsmeow's sqlstore.Container). If the client is not paired,
// falls back to the overlay (which tests can seed).
//
// Per ports.go §SessionStore: returns a zero Session (NOT an error) when
// no session exists.
func (a *Adapter) Load(ctx context.Context) (domain.Session, error) {
	if err := ctx.Err(); err != nil {
		return domain.Session{}, err
	}
	// Prefer the live whatsmeow device when available.
	if sess, ok := a.liveSession(); ok {
		return sess, nil
	}
	// Fall back to the overlay (used by tests and for not-yet-paired state).
	a.overlayMu.Lock()
	defer a.overlayMu.Unlock()
	return a.seedSession, nil
}

// liveSession extracts a domain.Session from the whatsmeow client's
// device store, returning false if no paired device is available.
func (a *Adapter) liveSession() (domain.Session, bool) {
	if a.client == nil {
		return domain.Session{}, false
	}
	device := a.client.Store()
	if device == nil || device.ID == nil {
		return domain.Session{}, false
	}
	bare := device.ID.ToNonAD()
	jid, err := toDomain(bare)
	if err != nil {
		return domain.Session{}, false
	}
	// NewSession requires deviceID > 0; default to 1 if whatsmeow
	// reports 0 (primary device before full registration).
	devID := device.ID.Device
	if devID == 0 {
		devID = 1
	}
	// createdAt is the pairing instant, read from the store — never
	// time.Now(). Minting it here made every health poll report a
	// sessionSince of "right now", so a field whose only job is to say how
	// long the pairing has survived instead said nothing while reading as
	// evidence (issue #311). Zero when unknown; domain.Session accepts
	// that and health omits the field.
	sess, err := domain.NewSession(jid, devID, a.cachedPairedAt())
	if err != nil {
		return domain.Session{}, false
	}
	return sess, true
}

// cachedPairedAt returns the pairing instant, or the zero time when it is
// unknown (nothing paired, no pairingClock wired, or a session paired
// before the timestamp was recorded at all).
func (a *Adapter) cachedPairedAt() time.Time {
	a.pairedAtMu.Lock()
	defer a.pairedAtMu.Unlock()
	return a.pairedAt
}

// loadPairedAt seeds the cache from the session container at Open. A read
// failure is logged and treated as unknown: a daemon that cannot read one
// cosmetic timestamp must still start and serve messages.
func (a *Adapter) loadPairedAt(ctx context.Context) {
	clock, ok := a.session.(pairingClock)
	if !ok {
		return
	}
	ts, known, err := clock.PairedAt(ctx)
	if err != nil {
		a.logger.Warn("read pairing timestamp", "err", err)
		return
	}
	if !known {
		return
	}
	a.pairedAtMu.Lock()
	a.pairedAt = ts.UTC()
	a.pairedAtMu.Unlock()
}

// recordPairedAt persists and caches the pairing instant. Called from the
// event handler on events.PairSuccess — the one moment the daemon knows
// the handshake just completed. Persistence is best-effort for the same
// reason as loadPairedAt, and the cache is written either way so the
// running process reports the truth even if the write failed.
func (a *Adapter) recordPairedAt(ctx context.Context, t time.Time) {
	t = t.UTC()
	a.pairedAtMu.Lock()
	a.pairedAt = t
	a.pairedAtMu.Unlock()

	clock, ok := a.session.(pairingClock)
	if !ok {
		return
	}
	if err := clock.SetPairedAt(ctx, t); err != nil {
		a.logger.Warn("persist pairing timestamp", "err", err)
	}
}

// Save implements app.SessionStore. Writes to the overlay.
func (a *Adapter) Save(ctx context.Context, s domain.Session) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	a.overlayMu.Lock()
	defer a.overlayMu.Unlock()
	a.seedSession = s
	return nil
}

// Clear implements app.SessionStore. Idempotent.
func (a *Adapter) Clear(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return a.clearSessionLocked(ctx)
}

// clearSessionLocked is the internal helper that resets the overlay
// session and (when a real sessionContainer is wired) delegates clearing
// to it. The overlay-only path is enough for unit tests.
func (a *Adapter) clearSessionLocked(ctx context.Context) error {
	// The pairing timestamp describes the device that just went away, so
	// it goes with it — otherwise the next pairing that somehow skipped
	// PairSuccess would inherit the old session's age.
	a.recordPairedAt(ctx, time.Time{})

	a.overlayMu.Lock()
	defer a.overlayMu.Unlock()
	a.seedSession = domain.Session{}
	return nil
}

// Logout calls the upstream whatsmeow Logout (server-side device unlink).
// It is exposed for the composition root's handlePanic to invoke directly.
// If the client is nil or already closed, Logout returns nil.
func (a *Adapter) Logout(ctx context.Context) error {
	if a.closed.Load() || a.client == nil {
		return nil
	}
	return a.client.Logout(ctx)
}

// WarmupSince returns the instant the rate limiter should treat as the
// start of this session's life, and whether a device is paired at all.
//
// It exists because the limiter and health's sessionSince ask different
// questions of the same missing data, and only one of them may answer with
// a substitute:
//
//   - sessionSince asks "when did the handshake happen". For a session
//     paired before issue #311 that is genuinely unknown, and #311 settled
//     that health reports the absence rather than a number that reads as
//     evidence. Untouched here.
//   - the limiter asks "is this account new enough to need throttling".
//     Mapping "unknown" to time.Now() there re-pinned warmup day 0 on every
//     restart, holding a months-old account at 25% of its rate ladder
//     forever — issue #368, measured live as 9 calls through then a wall of
//     -32014.
//
// So: the real pairing instant when it is known; otherwise a durable
// "first boot at which we observed this pairing", adopted once and
// persisted. Nothing is fabricated, it converges after one boot, and a
// genuinely new session still warms up from its own first sighting.
//
// Persistence is best-effort. A store that cannot record the epoch yields
// a per-boot value, which is exactly the old behaviour — degraded, not
// broken.
func (a *Adapter) WarmupSince(ctx context.Context, now time.Time) (time.Time, bool) {
	_, paired := a.liveSession()
	if !paired {
		// Nothing paired: the next handshake is genuinely session start,
		// and recordPairedAt will overwrite this on events.PairSuccess.
		return now, false
	}
	if ts := a.cachedPairedAt(); !ts.IsZero() {
		return ts, true
	}

	clock, ok := a.session.(warmupClock)
	if !ok {
		return now, true
	}
	ts, known, err := clock.WarmupEpoch(ctx)
	if err != nil {
		a.logger.Warn("read warmup epoch", "err", err)
		return now, true
	}
	if known {
		return ts, true
	}
	if err := clock.SetWarmupEpoch(ctx, now.UTC()); err != nil {
		a.logger.Warn("persist warmup epoch", "err", err)
	}
	return now, true
}
