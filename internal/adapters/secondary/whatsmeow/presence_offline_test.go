package whatsmeow

import (
	"testing"
	"time"

	waTypes "go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"
)

// connectedAdapter builds an adapter with presence-offline set to offline,
// dispatches a Connected event, and returns the fake for assertion. Shared by
// both presence tests so neither reproduces the standard setup block.
func connectedAdapter(t *testing.T, offline bool) *fakeWhatsmeowClient {
	t.Helper()
	fc := newFakeClient()
	a := newTestAdapter(t, fc)
	t.Cleanup(func() { _ = a.Close() })
	a.SetPresenceOffline(offline)
	fc.dispatch(&events.Connected{})
	return fc
}

// presenceCallCount reads the fake's recorded SendPresence count under lock.
func presenceCallCount(fc *fakeWhatsmeowClient) int {
	fc.mu.Lock()
	defer fc.mu.Unlock()
	return len(fc.PresenceCalls)
}

// With presence-offline enabled, a Connected event makes the daemon announce
// PresenceUnavailable exactly once (PR #280). The announce is best-effort on a
// goroutine, so poll briefly.
func TestPresenceOffline_AnnouncesUnavailableOnConnect(t *testing.T) {
	fc := connectedAdapter(t, true)

	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) && presenceCallCount(fc) == 0 {
		<-time.After(2 * time.Millisecond)
	}

	fc.mu.Lock()
	defer fc.mu.Unlock()
	if len(fc.PresenceCalls) != 1 || fc.PresenceCalls[0] != waTypes.PresenceUnavailable {
		t.Fatalf("want one SendPresence(unavailable); got %v", fc.PresenceCalls)
	}
}

// Default (presence-offline OFF): a Connected event announces nothing —
// presence-subscribe paths stay untouched.
func TestPresenceOffline_DisabledNoAnnounce(t *testing.T) {
	fc := connectedAdapter(t, false)
	<-time.After(50 * time.Millisecond) // give any (erroneous) goroutine time to fire

	if n := presenceCallCount(fc); n != 0 {
		t.Errorf("presence-offline off: want 0 SendPresence calls; got %d", n)
	}
}
