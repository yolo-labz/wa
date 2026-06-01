package whatsmeow

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/yolo-labz/wa/v2/internal/domain"
)

// TestReconnect_DisconnectThenConnectSameDevice is the end-to-end proof
// of the soft-stale recovery ACTION (spec 110g recover extension, PR
// #212). It drives the real Adapter — the same Reconnect the watchdog
// invokes on a healthy->stale edge — and asserts the recovery does
// exactly the right thing on the live client:
//
//   - exactly one Disconnect() followed by one Connect() (a fresh Noise
//     handshake — what breaks a zombie link);
//   - zero Logout() calls, so the paired device in session.db is
//     untouched: the SAME device reconnects, no QR re-scan;
//   - the client ends up connected;
//   - the event pipeline survives the reconnect cycle, so inbound
//     resumes (an event enqueued after the reconnect is still delivered).
//
// Together with TestSoftStale_RecoverFiresOncePerEdgeWithCooldown
// (cmd/wad — proves the watchdog fires this action exactly once per edge
// and honours the cooldown) this closes the loop the watchdog opens.
func TestReconnect_DisconnectThenConnectSameDevice(t *testing.T) {
	fc := newFakeClient()
	// The zombie reports connected:true while delivering nothing — start
	// "connected" so the test mirrors the real precondition.
	fc.ConnectedFlag = true
	a := newTestAdapter(t, fc)
	t.Cleanup(func() { _ = a.Close() })

	disBefore, conBefore := fc.DisconnectCnt, fc.ConnectCalls

	if err := a.Reconnect(context.Background()); err != nil {
		t.Fatalf("Reconnect: unexpected error %v", err)
	}

	if got := fc.DisconnectCnt - disBefore; got != 1 {
		t.Errorf("Disconnect calls = %d, want 1", got)
	}
	if got := fc.ConnectCalls - conBefore; got != 1 {
		t.Errorf("Connect calls = %d, want 1", got)
	}
	if fc.LogoutCalls != 0 {
		t.Errorf("Logout calls = %d, want 0 (same device — no re-pair / QR)", fc.LogoutCalls)
	}
	if !fc.ConnectedFlag {
		t.Error("client is not connected after Reconnect")
	}

	// Inbound resumes: an event enqueued after the reconnect is still
	// delivered on the adapter's stream — the recover cycle did not tear
	// the pipeline down.
	a.EnqueueEvent(domain.ConnectionEvent{ID: "post-reconnect", TS: time.Now(), State: domain.ConnConnected})
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if _, err := a.Next(ctx); err != nil {
		t.Fatalf("inbound did not resume after reconnect: %v", err)
	}
}

// TestReconnect_PropagatesConnectError proves the failure path: when the
// fresh Connect() fails, Reconnect wraps and returns the error (so the
// watchdog logs "reconnect failed" rather than claiming success), and the
// Disconnect() still fired first — the link is torn down even on a failed
// re-dial, leaving whatsmeow's own reconnect loop to retry.
func TestReconnect_PropagatesConnectError(t *testing.T) {
	fc := newFakeClient()
	fc.ConnectErr = errors.New("dial whatsapp: i/o timeout")
	a := newTestAdapter(t, fc)
	t.Cleanup(func() { _ = a.Close() })

	err := a.Reconnect(context.Background())
	if err == nil {
		t.Fatal("Reconnect: want error when Connect fails, got nil")
	}
	if !errors.Is(err, fc.ConnectErr) {
		t.Errorf("error = %v, want a wrap of ConnectErr", err)
	}
	if fc.DisconnectCnt != 1 {
		t.Errorf("Disconnect calls = %d, want 1 (tears down before the failed re-dial)", fc.DisconnectCnt)
	}
}

// TestReconnect_RespectsCancelledContext proves a cancelled context is a
// no-op: Reconnect returns the cancellation and never touches the client,
// so a shutdown mid-stale cannot race a reconnect against teardown.
func TestReconnect_RespectsCancelledContext(t *testing.T) {
	fc := newFakeClient()
	a := newTestAdapter(t, fc)
	t.Cleanup(func() { _ = a.Close() })

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if err := a.Reconnect(ctx); !errors.Is(err, context.Canceled) {
		t.Errorf("Reconnect on cancelled ctx = %v, want context.Canceled", err)
	}
	if fc.DisconnectCnt != 0 || fc.ConnectCalls != 0 {
		t.Errorf("cancelled ctx must not touch the client: Disconnect=%d Connect=%d, want 0/0",
			fc.DisconnectCnt, fc.ConnectCalls)
	}
}
