package whatsmeow

import (
	"testing"
	"time"

	waEvents "go.mau.fi/whatsmeow/types/events"
)

// TestClose_WaitsForLoggedOutPanicGoroutine asserts that Adapter.Close()
// blocks until the fire-and-forget goroutine spawned for events.LoggedOut
// has completed Panic(). Before PR #136, Close raced with that goroutine
// and could close the SQLite containers while Panic was still wiping
// them. The fix adds a panicWg and a Wait() in Close after the history
// sync wait. The test injects a 200 ms delay into fake.Logout (which
// Panic calls in step 1) and asserts Close's wall-clock elapsed is at
// least that delay — proving the goroutine ran to completion under
// Close's wait.
func TestClose_WaitsForLoggedOutPanicGoroutine(t *testing.T) {
	t.Parallel()

	const logoutDelay = 200 * time.Millisecond

	started := make(chan struct{})
	fc := newFakeClient()
	fc.LogoutHook = func() {
		close(started)
		time.Sleep(logoutDelay)
	}
	a := newTestAdapter(t, fc)
	a.SetPanicArtefacts(artefactTempPaths(t))

	// Spawn the panic goroutine via the LoggedOut event path.
	a.handleWAEvent(&waEvents.LoggedOut{OnConnect: false})

	// Wait until the goroutine is inside fake.Logout (has begun the
	// logoutDelay sleep). Bound the wait so a regression that fails
	// to spawn the goroutine surfaces as a test failure rather than
	// a hang.
	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("LoggedOut handler never invoked Logout — goroutine did not spawn")
	}

	start := time.Now()
	if err := a.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	elapsed := time.Since(start)

	if elapsed < logoutDelay {
		t.Fatalf("Close returned in %v, expected ≥%v — panicWg.Wait() did not block on the LoggedOut goroutine",
			elapsed, logoutDelay)
	}

	fc.mu.Lock()
	calls := fc.LogoutCalls
	fc.mu.Unlock()
	if calls != 1 {
		t.Fatalf("LogoutCalls=%d, want 1 — Close should have waited for the goroutine to invoke Logout exactly once", calls)
	}
}
