package admin

import (
	"bytes"
	"context"
	"errors"
	"sync"
	"time"

	"github.com/yolo-labz/wa/v2/internal/app"
)

// pairFlow is the admin pairing state machine (spec 116 SR-116-06): one
// flow per daemon, owned by the admin session that started it. Each start
// is a new generation; the QR sink handed to the pair job is bound to its
// generation, so a code that arrives after a cancel or a restart is
// dropped. The QR payload lives only here, only while the state is
// showing_qr, and leaves only through a single-use nonce. Routes, audit
// kinds and the cancellation triggers attach in S6a2.
type pairFlow struct {
	now    func() time.Time
	record func(cause string) error // appends pair_cancel{cause}

	mu     sync.Mutex
	gen    uint64
	owner  string // admin session id
	state  pairState
	cause  string
	code   []byte               // current payload, showing_qr only
	nonces map[string]time.Time // nonce -> expiry
	stop   context.CancelFunc   // cancels the pair job
}

type pairState string

const (
	pairIdle      pairState = "idle"
	pairStarting  pairState = "starting"
	pairShowing   pairState = "showing_qr"
	pairPaired    pairState = "paired"
	pairFailed    pairState = "failed"
	pairCancelled pairState = "cancelled"
)

// pairNonceTTL bounds one QR image URL (SR-116-06: at most 60 s).
const pairNonceTTL = 60 * time.Second

var errPairBusy = errors.New("a pairing is already in progress")

func newPairFlow(now func() time.Time, record func(cause string) error) *pairFlow {
	return &pairFlow{now: now, record: record, state: pairIdle, nonces: map[string]time.Time{}}
}

// begin starts a new generation for owner; stop cancels its pair job. The
// sink goes on the job's ctx (app.WithQRSink); gen identifies the job's
// result to end.
func (f *pairFlow) begin(owner string, stop context.CancelFunc) (gen uint64, sink app.QRSink, err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.liveLocked() {
		return 0, nil, errPairBusy
	}
	f.gen++
	gen = f.gen
	f.owner, f.state, f.cause, f.stop = owner, pairStarting, "", stop
	clear(f.nonces)
	return gen, func(code string) { f.show(gen, code) }, nil
}

func (f *pairFlow) liveLocked() bool { return f.state == pairStarting || f.state == pairShowing }

// show replaces the payload with code; a stale generation or an ended
// flow drops it before anything can render it.
func (f *pairFlow) show(gen uint64, code string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if gen != f.gen || !f.liveLocked() {
		return
	}
	f.wipeLocked()
	f.code, f.state = []byte(code), pairShowing
}

// mint issues a single-use, 60 s nonce for owner's current QR.
func (f *pairFlow) mint(owner string) (string, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.state != pairShowing || owner != f.owner {
		return "", false
	}
	n := randomID()
	f.nonces[n] = f.now().Add(pairNonceTTL)
	return n, true
}

// take redeems nonce once and returns a copy of the payload; the caller
// zeroes it after encoding the image.
func (f *pairFlow) take(owner, nonce string) ([]byte, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	exp, ok := f.nonces[nonce]
	delete(f.nonces, nonce)
	if !ok || owner != f.owner || f.state != pairShowing || !f.now().Before(exp) {
		return nil, false
	}
	return bytes.Clone(f.code), true
}

// cancel ends a live flow in the SR-116-06 order: cancel the pair job,
// invalidate every nonce, zero and drop the payload, then record cause.
// The first cause wins; on an ended flow it is a no-op. A record failure
// is returned, but the flow stays cancelled.
func (f *pairFlow) cancel(cause string) error {
	f.mu.Lock()
	if !f.liveLocked() {
		f.mu.Unlock()
		return nil
	}
	f.state, f.cause = pairCancelled, cause
	f.endLocked()
	f.mu.Unlock()
	return f.record(cause)
}

// end takes the pair job's result for generation gen. A cancelled flow or
// a newer generation is left as it is.
func (f *pairFlow) end(gen uint64, err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if gen != f.gen || !f.liveLocked() {
		return
	}
	f.state = pairPaired
	if err != nil {
		f.state = pairFailed
	}
	f.endLocked()
}

func (f *pairFlow) endLocked() {
	if f.stop != nil {
		f.stop()
		f.stop = nil
	}
	clear(f.nonces)
	f.wipeLocked()
}

// wipeLocked zeroes the flow's copy of the payload and drops it. The
// string whatsmeow handed the sink is immutable and outside this reach.
func (f *pairFlow) wipeLocked() {
	clear(f.code)
	f.code = nil
}
