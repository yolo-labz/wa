package whatsmeow

import (
	"sync/atomic"
	"testing"
	"time"

	waEvents "go.mau.fi/whatsmeow/types/events"
)

// TestClose_WaitsForLoggedOutPanicGoroutine asserts that Adapter.Close()
// blocks until the fire-and-forget goroutine spawned for events.LoggedOut
// has completed Panic(). Before PR #136, Close raced with that goroutine
// and could close the SQLite containers while Panic was still wiping
// them. The fix adds a panicWg and a Wait() in Close after the history
// sync wait.
//
// The test injects a 200 ms delay into fake.Logout (which Panic calls in
// step 1) so the goroutine is demonstrably still in flight when Close
// starts, then asserts the goroutine's completion flag is set once Close
// returns. The assertion is ORDER-based, not clock-based: an earlier
// version compared Close's wall-clock elapsed against the injected delay,
// but the sleep starts before the elapsed stamp does, so a few ms of
// scheduler delay between the two made it flake on loaded CI runners
// (observed: 197.5 ms < 200 ms on PR #265's run — the sleep had simply
// started ~2.5 ms before the stamp).
func TestClose_WaitsForLoggedOutPanicGoroutine(t *testing.T) {
	t.Parallel()

	const logoutDelay = 200 * time.Millisecond

	started := make(chan struct{})
	var logoutDone atomic.Bool
	fc := newFakeClient()
	fc.LogoutHook = func() {
		close(started)
		time.Sleep(logoutDelay)
		logoutDone.Store(true)
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

	if err := a.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	// Happens-after proof: the hook's final store must be visible once
	// Close returns. If panicWg.Wait() were missing, Close would return
	// while the goroutine is still inside the 200 ms sleep and the flag
	// would still be false.
	if !logoutDone.Load() {
		t.Fatal("Close returned before the LoggedOut goroutine finished — panicWg.Wait() did not join it")
	}

	fc.mu.Lock()
	calls := fc.LogoutCalls
	fc.mu.Unlock()
	if calls != 1 {
		t.Fatalf("LogoutCalls=%d, want 1 — Close should have waited for the goroutine to invoke Logout exactly once", calls)
	}
}
