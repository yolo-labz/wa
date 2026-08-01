package whatsmeow

// pair_test.go (T034) exercises Adapter.Pair against the in-package
// fakeWhatsmeowClient. Four cases per the commit-5 spec:
//
//  1. Existing session reused — a device ID on file => Pair returns nil
//     and never touches the QR channel or Connect.
//  2. QR flow happy path — fake feeds {code} then {success} on the
//     QR channel, Pair returns nil.
//  3. Phone-code flow happy path — fake returns a linking code from
//     PairPhone, the test signals pairSuccessCh by dispatching a
//     synthetic events.PairSuccess through the fake's handler list.
//  4. Pair context timeout — pairTimeout shortened to 50ms, fake never
//     produces success, Pair must return a wrapped DeadlineExceeded.
//
// Issue #310 adds three more, all about the split-brain daemon that
// answered `paired: true` while `health` on the same process answered
// `paired: false`: the stale IsLoggedIn flag must not stand in for a
// session, and a device this process retired must refuse rather than
// pretend.

import (
	"context"
	"errors"
	"testing"
	"time"

	waClient "go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/store"
	waEvents "go.mau.fi/whatsmeow/types/events"

	"github.com/yolo-labz/wa/v2/internal/domain"
)

// withShortPairTimeout temporarily shrinks the package-level pairTimeout
// for the duration of a single test, restoring it via t.Cleanup. Used by
// the timeout case so the test runs in milliseconds, not minutes.
func withShortPairTimeout(t *testing.T, d time.Duration) {
	t.Helper()
	prev := pairTimeout
	pairTimeout = d
	t.Cleanup(func() { pairTimeout = prev })
}

func TestPair_ExistingSessionReused(t *testing.T) {
	fake := newFakeClient()
	// A device identity on file is what "already paired" means. The
	// IsLoggedIn flag rides along because a live session is normally
	// connected, but the store is the thing Pair reads.
	fake.Device = fakeDeviceWithJID(t, "5511999999999@s.whatsapp.net")
	fake.LoggedInFlag = true
	a := openWithClient(fake, nil, nil, nil)
	t.Cleanup(func() { _ = a.Close() })

	if err := a.Pair(context.Background(), ""); err != nil {
		t.Fatalf("Pair returned error: %v", err)
	}
	if fake.ConnectCalls != 0 {
		t.Errorf("Connect should not be called when already logged in; got %d calls", fake.ConnectCalls)
	}
	if fake.PairPhoneCall != nil {
		t.Errorf("PairPhone should not be called when already logged in")
	}
}

// TestPair_StaleLoggedInFlagStillPairs is the issue #310 regression.
// whatsmeow clears the in-memory isLoggedIn atomic in exactly one place
// (handleStreamError); Logout and Disconnect leave it set. Pair used to
// short-circuit on that flag, so a daemon whose store had been emptied
// answered `paired: true` having done nothing at all — while `health` on
// the same process read the store and answered `paired: false`. With an
// empty store, Pair must run the real flow.
func TestPair_StaleLoggedInFlagStillPairs(t *testing.T) {
	fake := newFakeClient()
	fake.LoggedInFlag = true // stale: no device behind it
	qr := make(chan waClient.QRChannelItem, 2)
	qr <- waClient.QRChannelItem{Event: "code", Code: "test-code-payload"}
	qr <- waClient.QRChannelItem{Event: "success"}
	close(qr)
	fake.QRChan = qr

	a := openWithClient(fake, nil, nil, nil)
	t.Cleanup(func() { _ = a.Close() })

	if err := a.Pair(context.Background(), ""); err != nil {
		t.Fatalf("Pair returned error: %v", err)
	}
	if fake.ConnectCalls != 1 {
		t.Errorf("stale IsLoggedIn must not short-circuit pairing; Connect calls = %d, want 1", fake.ConnectCalls)
	}
}

// TestPair_RetiredDeviceRefuses covers the `session.logout` half of
// issue #310 — the one `wa pair --reset` issues before pairing.
// whatsmeow's store.Device.Delete retires the device in place (ID nil,
// Deleted true, every sub-store swapped for a NoopStore), and the adapter
// acquires its device exactly once at Open, so no handshake can succeed
// in this process again. Refusing by name beats a silent success or an
// undiagnosable -32603.
func TestPair_RetiredDeviceRefuses(t *testing.T) {
	fake := newFakeClient()
	fake.LoggedInFlag = true
	fake.Device = &store.Device{Deleted: true}

	a := openWithClient(fake, nil, nil, nil)
	t.Cleanup(func() { _ = a.Close() })

	err := a.Pair(context.Background(), "")
	if !errors.Is(err, domain.ErrSessionWiped) {
		t.Fatalf("Pair err = %v, want domain.ErrSessionWiped", err)
	}
	if fake.ConnectCalls != 0 {
		t.Errorf("Connect should not be attempted against a retired device; got %d calls", fake.ConnectCalls)
	}
}

// TestPair_AfterPanicRefuses covers the `wa panic` half. Panic deletes
// session.db off disk; whether the server-side Logout succeeded first
// decides whether the in-memory device still looks populated, so the
// refusal keys off panicDone rather than the store alone.
func TestPair_AfterPanicRefuses(t *testing.T) {
	fake := newFakeClient()
	// Device still populated in memory: the artefacts are gone from disk
	// but nothing retired this *store.Device, which is exactly the case a
	// store-only check would miss.
	fake.Device = fakeDeviceWithJID(t, "5511999999999@s.whatsapp.net")

	a := newTestAdapter(t, fake)
	t.Cleanup(func() { _ = a.Close() })

	if err := a.Panic(context.Background(), "test"); err != nil {
		t.Fatalf("Panic: %v", err)
	}

	err := a.Pair(context.Background(), "")
	if !errors.Is(err, domain.ErrSessionWiped) {
		t.Fatalf("Pair err = %v, want domain.ErrSessionWiped", err)
	}
}

func TestPair_QRHappyPath(t *testing.T) {
	fake := newFakeClient()
	// Replace the default closed QR channel with one we can feed.
	qr := make(chan waClient.QRChannelItem, 2)
	qr <- waClient.QRChannelItem{Event: "code", Code: "test-code-payload"}
	qr <- waClient.QRChannelItem{Event: "success"}
	close(qr)
	fake.QRChan = qr

	a := openWithClient(fake, nil, nil, nil)
	t.Cleanup(func() { _ = a.Close() })

	if err := a.Pair(context.Background(), ""); err != nil {
		t.Fatalf("Pair returned error: %v", err)
	}
	if fake.ConnectCalls != 1 {
		t.Errorf("expected Connect called once; got %d", fake.ConnectCalls)
	}
}

func TestPair_PhoneCodeHappyPath(t *testing.T) {
	fake := newFakeClient()
	fake.PairCode = "ABCD-1234"

	a := openWithClient(fake, nil, nil, nil)
	t.Cleanup(func() { _ = a.Close() })

	// Spawn a goroutine that waits long enough for Pair() to be
	// blocked on pairSuccessCh, then dispatches a synthetic PairSuccess
	// event through the fake's handler list. handleWAEvent will see
	// the PairingEvent and signal pairSuccessCh.
	done := make(chan error, 1)
	go func() {
		done <- a.Pair(context.Background(), "+5511999999999")
	}()

	// Give Pair() a moment to invoke PairPhone and start blocking.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		fake.mu.Lock()
		called := fake.PairPhoneCall != nil
		fake.mu.Unlock()
		if called {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}

	// Dispatch the upstream PairSuccess; handleWAEvent will signal
	// pairSuccessCh which unblocks Pair.
	fake.dispatch(&waEvents.PairSuccess{})

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Pair returned error: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Pair did not return after PairSuccess dispatch")
	}
	if fake.PairPhoneCall == nil {
		t.Fatal("expected PairPhone to be called")
	}
	if fake.PairPhoneCall.Phone != "+5511999999999" {
		t.Errorf("expected phone +5511999999999; got %q", fake.PairPhoneCall.Phone)
	}
	if fake.PairPhoneCall.ClientType != waClient.PairClientChrome {
		t.Errorf("expected PairClientChrome; got %v", fake.PairPhoneCall.ClientType)
	}
	if fake.PairPhoneCall.ClientDisplayName != "wad" {
		t.Errorf("expected display name wad; got %q", fake.PairPhoneCall.ClientDisplayName)
	}
}

func TestPair_ContextTimeout(t *testing.T) {
	withShortPairTimeout(t, 50*time.Millisecond)

	fake := newFakeClient()
	fake.PairCode = "TIMEOUT0"
	// Phone-code flow blocks on pairSuccessCh which never gets signaled.
	a := openWithClient(fake, nil, nil, nil)
	t.Cleanup(func() { _ = a.Close() })

	err := a.Pair(context.Background(), "+5511000000000")
	if err == nil {
		t.Fatal("expected timeout error; got nil")
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("expected wrapped DeadlineExceeded; got %v", err)
	}
}
