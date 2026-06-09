package whatsmeow

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/yolo-labz/wa/v2/internal/domain"
)

// Already connected + logged in → BackfillRecent fires a global on-demand
// pull immediately (no wait) and forwards historyRoundTripCap as the count.
func TestBackfillRecent_LoggedIn_FiresGlobalPull(t *testing.T) {
	fc := newFakeClient()
	fc.ConnectedFlag = true
	fc.LoggedInFlag = true
	a := newTestAdapter(t, fc)
	t.Cleanup(func() { _ = a.Close() })
	withNewest(a, domain.MustJID("15551234567@s.whatsapp.net")) // one recent chat to anchor the global sweep

	r, err := a.BackfillRecent(context.Background())
	if err != nil {
		t.Fatalf("BackfillRecent: %v", err)
	}
	if !r.Async || !r.Connected || !r.Requested {
		t.Errorf("want Async+Connected+Requested true; got %+v", r)
	}
	if r.Chat != "" {
		t.Errorf("want global (empty) chat; got %q", r.Chat)
	}
	if len(fc.BuildHSReqs) != 1 {
		t.Fatalf("want 1 BuildHistorySyncRequest; got %d", len(fc.BuildHSReqs))
	}
	if got := fc.BuildHSReqs[0].Count; got != historyRoundTripCap {
		t.Errorf("want count=%d (cap); got %d", historyRoundTripCap, got)
	}
}

// Login lands AFTER the call starts → BackfillRecent waits, then fires the
// pull once IsLoggedIn flips. Exercises the bounded poll loop.
func TestBackfillRecent_WaitsForLogin(t *testing.T) {
	fc := newFakeClient()
	fc.ConnectedFlag = true
	fc.LoggedInFlag = false // not yet logged in
	a := newTestAdapter(t, fc)
	t.Cleanup(func() { _ = a.Close() })
	a.backfillLoginWait = 2 * time.Second
	a.backfillPollEvery = 5 * time.Millisecond
	withNewest(a, domain.MustJID("15551234567@s.whatsapp.net")) // one recent chat to anchor the global sweep

	// Flip to logged-in shortly after the call starts (race-safe setter).
	go func() {
		<-time.After(20 * time.Millisecond)
		fc.setLoggedIn(true)
	}()

	r, err := a.BackfillRecent(context.Background())
	if err != nil {
		t.Fatalf("BackfillRecent: %v", err)
	}
	if !r.Requested || len(fc.BuildHSReqs) != 1 {
		t.Errorf("want one pull after login landed; got Requested=%v reqs=%d", r.Requested, len(fc.BuildHSReqs))
	}
}

// Login never settles within the wait → typed ErrDisconnected and no pull.
func TestBackfillRecent_LoginTimeout(t *testing.T) {
	fc := newFakeClient()
	fc.ConnectedFlag = true
	fc.LoggedInFlag = false
	a := newTestAdapter(t, fc)
	t.Cleanup(func() { _ = a.Close() })
	a.backfillLoginWait = 30 * time.Millisecond
	a.backfillPollEvery = 5 * time.Millisecond

	_, err := a.BackfillRecent(context.Background())
	if !errors.Is(err, domain.ErrDisconnected) {
		t.Fatalf("want ErrDisconnected on login timeout; got %v", err)
	}
	if len(fc.BuildHSReqs) != 0 {
		t.Errorf("must not pull when login never settled; got %d reqs", len(fc.BuildHSReqs))
	}
}

// ctx cancellation while waiting for login returns the ctx error promptly
// and issues no pull.
func TestBackfillRecent_CtxCancelled(t *testing.T) {
	fc := newFakeClient()
	fc.ConnectedFlag = true
	fc.LoggedInFlag = false
	a := newTestAdapter(t, fc)
	t.Cleanup(func() { _ = a.Close() })
	a.backfillLoginWait = 5 * time.Second
	a.backfillPollEvery = 5 * time.Millisecond

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		<-time.After(20 * time.Millisecond)
		cancel()
	}()

	_, err := a.BackfillRecent(ctx)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("want context.Canceled; got %v", err)
	}
	if len(fc.BuildHSReqs) != 0 {
		t.Errorf("must not pull when ctx cancelled; got %d reqs", len(fc.BuildHSReqs))
	}
}
