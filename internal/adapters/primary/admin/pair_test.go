package admin

import (
	"errors"
	"sync"
	"testing"
	"time"
)

type flowFixture struct {
	f       *pairFlow
	clock   time.Time
	stopped int
	causes  []string
	failRec error
	atRec   func()
}

func newFlowFixture() *flowFixture {
	x := &flowFixture{clock: time.Unix(1_800_000_000, 0)}
	x.f = newPairFlow(func() time.Time { return x.clock }, func(cause string) error {
		if x.atRec != nil {
			x.atRec()
		}
		x.causes = append(x.causes, cause)
		return x.failRec
	})
	return x
}

func (x *flowFixture) stop() { x.stopped++ }

func (x *flowFixture) state() (pairState, []byte, int) {
	x.f.mu.Lock()
	defer x.f.mu.Unlock()
	return x.f.state, x.f.code, len(x.f.nonces)
}

// TestAdminPair_LateCodeEventDropped (SR-116-06): a code from a cancelled
// flow, or from an older generation while a newer one runs, is dropped
// before anything can render it, and the old job's result is ignored.
func TestAdminPair_LateCodeEventDropped(t *testing.T) {
	x := newFlowFixture()
	gen1, sink1, err := x.f.begin("s1", x.stop)
	if err != nil {
		t.Fatal(err)
	}
	sink1("code-1")
	if st, code, _ := x.state(); st != pairShowing || string(code) != "code-1" {
		t.Fatalf("first code: %s %q", st, code)
	}
	if err := x.f.cancel("explicit"); err != nil {
		t.Fatal(err)
	}
	sink1("late-after-cancel")
	if st, code, _ := x.state(); st != pairCancelled || code != nil {
		t.Fatalf("late code revived the flow: %s %q", st, code)
	}
	if _, ok := x.f.mint("s1"); ok {
		t.Fatal("a cancelled flow minted a QR URL")
	}

	gen2, sink2, err := x.f.begin("s1", x.stop)
	if err != nil || gen2 == gen1 {
		t.Fatalf("restart: gen %d after %d, %v", gen2, gen1, err)
	}
	sink1("stale-generation")
	if st, code, _ := x.state(); st != pairStarting || code != nil {
		t.Fatalf("an old generation's code reached the new flow: %s %q", st, code)
	}
	sink2("code-2")
	x.f.end(gen1, nil) // the old job finishing late
	n, _ := x.f.mint("s1")
	if got, ok := x.f.take("s1", n); !ok || string(got) != "code-2" {
		t.Fatalf("new generation serves %q %v, want code-2", got, ok)
	}
	if st, _, _ := x.state(); st != pairShowing {
		t.Fatalf("old job's result ended the new flow: %s", st)
	}
}

// TestAdminPair_CancelDropsQRReferences (SR-116-06): cancel stops the pair
// job, invalidates every nonce and zeroes the payload before it records
// the cause; nothing can fetch the QR afterwards, the first cause wins,
// and a failed record still leaves the flow cancelled.
func TestAdminPair_CancelDropsQRReferences(t *testing.T) {
	x := newFlowFixture()
	_, sink, _ := x.f.begin("s1", x.stop)
	sink("secret-qr-payload")
	n1, _ := x.f.mint("s1")
	n2, _ := x.f.mint("s1")
	_, held, _ := x.state() // the flow's own buffer
	x.atRec = func() {
		st, code, nonces := x.state()
		if x.stopped != 1 || nonces != 0 || code != nil || st != pairCancelled {
			t.Errorf("at record: stopped %d, %d nonces, code %q, %s; want everything dropped first", x.stopped, nonces, code, st)
		}
		for _, b := range held {
			if b != 0 {
				t.Errorf("payload buffer not zeroed before record: %q", held)
				break
			}
		}
	}
	x.failRec = errors.New("disk full")
	if err := x.f.cancel("explicit"); err == nil {
		t.Error("a failed cancel record was not reported")
	}
	for _, n := range []string{n1, n2} {
		if got, ok := x.f.take("s1", n); ok {
			t.Errorf("QR fetched after cancel: %q", got)
		}
	}
	x.atRec, x.failRec = nil, nil
	if err := x.f.cancel("session_logout"); err != nil || len(x.causes) != 1 || x.causes[0] != "explicit" || x.stopped != 1 {
		t.Errorf("second cancel: %v, causes %v, stops %d", err, x.causes, x.stopped)
	}
}

// TestPairFlow_NonceRules: a nonce works once, for its owner, within 60 s;
// a second flow cannot start while one is live; a finished job ends the
// flow and drops the payload.
func TestPairFlow_NonceRules(t *testing.T) {
	x := newFlowFixture()
	gen, sink, _ := x.f.begin("s1", x.stop)
	if _, _, err := x.f.begin("s2", x.stop); !errors.Is(err, errPairBusy) {
		t.Fatalf("second flow: %v", err)
	}
	if _, ok := x.f.mint("s1"); ok {
		t.Fatal("minted before any code")
	}
	sink("qr")
	n, _ := x.f.mint("s1")
	if _, ok := x.f.mint("s2"); ok {
		t.Error("another session minted a QR URL")
	}
	if _, ok := x.f.take("s2", n); ok {
		t.Error("another session redeemed the nonce")
	}
	n, _ = x.f.mint("s1")
	if _, ok := x.f.take("s1", n); !ok {
		t.Fatal("owner's fresh nonce refused")
	}
	if _, ok := x.f.take("s1", n); ok {
		t.Error("nonce redeemed twice")
	}
	n, _ = x.f.mint("s1")
	x.clock = x.clock.Add(pairNonceTTL)
	if _, ok := x.f.take("s1", n); ok {
		t.Error("expired nonce redeemed")
	}
	n, _ = x.f.mint("s1")
	x.f.end(gen, nil)
	if st, code, nonces := x.state(); st != pairPaired || code != nil || nonces != 0 || x.stopped != 1 {
		t.Errorf("after success: %s %q %d nonces, stops %d", st, code, nonces, x.stopped)
	}
	if _, ok := x.f.take("s1", n); ok {
		t.Error("nonce outlived the flow")
	}
	if _, _, err := x.f.begin("s1", x.stop); err != nil {
		t.Errorf("new flow after success: %v", err)
	}
}

// TestPairFlow_CancelRacesCode (-race): codes arriving while cancel runs
// never leave a cancelled flow showing a QR.
func TestPairFlow_CancelRacesCode(t *testing.T) {
	for range 50 {
		x := newFlowFixture()
		_, sink, _ := x.f.begin("s1", func() {})
		var wg sync.WaitGroup
		wg.Go(func() {
			for range 20 {
				sink("qr")
			}
		})
		_ = x.f.cancel("timeout")
		wg.Wait()
		if st, code, _ := x.state(); st != pairCancelled || code != nil {
			t.Fatalf("after racing cancel: %s %q", st, code)
		}
	}
}

// TestPairFlow_NonceBoundToCode (p8 L2): a QR rotation invalidates the
// nonces minted for the previous code.
func TestPairFlow_NonceBoundToCode(t *testing.T) {
	x := newFlowFixture()
	_, sink, _ := x.f.begin("s1", x.stop)
	sink("first")
	n, _ := x.f.mint("s1")
	sink("second")
	if got, ok := x.f.take("s1", n); ok {
		t.Errorf("a nonce minted for the first code redeemed %q", got)
	}
	n, _ = x.f.mint("s1")
	if got, ok := x.f.take("s1", n); !ok || string(got) != "second" {
		t.Errorf("fresh nonce after rotation: %q %v", got, ok)
	}
}

// TestPairFlow_ExpiredNoncesPruned (p8 L1): minting drops expired nonces,
// so a live flow holds at most one TTL's worth.
func TestPairFlow_ExpiredNoncesPruned(t *testing.T) {
	x := newFlowFixture()
	_, sink, _ := x.f.begin("s1", x.stop)
	sink("qr")
	for range 100 {
		x.f.mint("s1")
		x.clock = x.clock.Add(time.Second)
	}
	if _, _, n := x.state(); n > int(pairNonceTTL/time.Second) {
		t.Errorf("%d nonces held after 100 s of minting, want at most %d", n, int(pairNonceTTL/time.Second))
	}
}
