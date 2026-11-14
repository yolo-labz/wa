package admin

import (
	"bytes"
	"context"
	"errors"
	"maps"
	"sync"
	"time"

	"github.com/yolo-labz/wa/v2/internal/app"
	"github.com/yolo-labz/wa/v2/internal/domain"
)

// pairFlow is the admin pairing state machine (spec 116 SR-116-06): one
// flow per daemon, owned by the admin session that started it. Each start
// is a new generation; the QR sink handed to the pair job is bound to its
// generation, so a code that arrives after a cancel or a restart is
// dropped. The QR payload lives only here, only while the state is
// showing_qr, and leaves only through a single-use nonce bound to that
// code: a rotation invalidates the nonces minted for the previous one, and
// the next meta refresh mints a fresh one. There is no separate expired
// state: the 3 min window ends a flow as cancelled with cause "timeout".
type pairFlow struct {
	now    func() time.Time
	record func(act domain.AuditAction, actor, cause string) error

	mu     sync.Mutex
	gen    uint64
	owner  string // admin session id
	actor  string // its backing token id, the audit actor
	state  pairState
	cause  string
	code   []byte               // current payload, showing_qr only
	nonces map[string]time.Time // nonce -> expiry
	stop   context.CancelFunc   // cancels the pair job
	live   bool                 // the generation's pair job has not exited (exited clears it)
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

func newPairFlow(now func() time.Time, record func(act domain.AuditAction, actor, cause string) error) *pairFlow {
	return &pairFlow{now: now, record: record, state: pairIdle, nonces: map[string]time.Time{}}
}

// begin starts a new generation for owner, acting as actor; stop cancels
// its pair job. The sink goes on the job's ctx (app.WithQRSink); gen
// identifies the job's result to end.
func (f *pairFlow) begin(owner, actor string, stop context.CancelFunc) (gen uint64, sink app.QRSink, err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.liveLocked() {
		return 0, nil, errPairBusy
	}
	f.gen++
	gen = f.gen
	f.owner, f.actor, f.state, f.cause, f.stop, f.live = owner, actor, pairStarting, "", stop, true
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
	clear(f.nonces)
	f.code, f.state = []byte(code), pairShowing
}

// mint issues a single-use, 60 s nonce for owner's current QR, first
// dropping expired ones so a live flow holds at most one TTL's worth.
func (f *pairFlow) mint(owner string) (string, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.state != pairShowing || owner != f.owner {
		return "", false
	}
	now := f.now()
	maps.DeleteFunc(f.nonces, func(_ string, exp time.Time) bool { return !now.Before(exp) })
	n := randomID()
	f.nonces[n] = now.Add(pairNonceTTL)
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

// view reports the flow's state and cause, and whether owner owns it.
func (f *pairFlow) view(owner string) (pairState, string, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.state, f.cause, f.owner == owner
}

func (f *pairFlow) cancel(cause string) error { return f.cancelIf(anyFlow, cause) }

func anyFlow(string, uint64) bool { return true }

func ownedBy(id string) func(string, uint64) bool {
	return func(owner string, _ uint64) bool { return owner == id }
}

// cancelIf ends a live flow whose owner and generation match, in the
// SR-116-06 order: cancel the pair job, invalidate every nonce, zero and
// drop the payload, then record pair_cancel{cause}. The first cause wins;
// on an ended flow it is a no-op. A record failure is returned, but the
// flow stays cancelled.
func (f *pairFlow) cancelIf(match func(owner string, gen uint64) bool, cause string) error {
	f.mu.Lock()
	if !f.liveLocked() || !match(f.owner, f.gen) {
		f.mu.Unlock()
		return nil
	}
	f.state, f.cause = pairCancelled, cause
	f.endLocked()
	actor := f.actor
	f.mu.Unlock()
	return f.record(domain.AuditPairCancel, actor, cause)
}

// exited records that generation gen's pair job is gone, so no later
// PairSuccess is this flow's (a CLI pairing's must not flip it).
func (f *pairFlow) exited(gen uint64) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if gen == f.gen {
		f.live = false
	}
}

// end takes the pair job's result for generation gen; a newer generation
// is left as it is. A success after a cancel or a failure cannot be undone
// (WhatsApp already linked the device): the flow becomes paired, the
// success is recorded, and end reports it, once.
func (f *pairFlow) end(gen uint64, err error) (afterCancel bool) {
	f.mu.Lock()
	if gen != f.gen || !f.liveLocked() {
		afterCancel = gen == f.gen && err == nil && (f.state == pairCancelled || f.state == pairFailed)
		if afterCancel {
			f.state = pairPaired
		}
		actor := f.actor
		f.mu.Unlock()
		if afterCancel {
			_ = f.record(domain.AuditPairSuccessAfterCancel, actor, "")
		}
		return afterCancel
	}
	defer f.mu.Unlock()
	f.state = pairPaired
	if err != nil {
		f.state = pairFailed
	}
	f.endLocked()
	return false
}

// succeeded is end for the current generation: the daemon saw PairSuccess.
// Once that generation's job has exited the event is someone else's.
func (f *pairFlow) succeeded() bool {
	f.mu.Lock()
	gen, live := f.gen, f.live
	f.mu.Unlock()
	return live && f.end(gen, nil)
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
