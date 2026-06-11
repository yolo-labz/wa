package main

import (
	"context"
	"path/filepath"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/yolo-labz/wa/v2/internal/domain"
)

// drainDebouncer counts how many times the debouncer's C() fires without
// blocking. It is only meaningful inside a settled synctest bubble where
// all timer goroutines are durably blocked.
func drainDebouncer(deb *allowlistDebouncer) int {
	n := 0
	for {
		select {
		case <-deb.C():
			n++
		default:
			return n
		}
	}
}

// TestAllowlistDebouncer_CoalescesBurst asserts the core debounce
// contract: a rapid burst of Trigger() calls within the window collapses
// to exactly one C() fire. Driven inside a testing/synctest bubble so the
// 100ms window is virtual time advanced deterministically by
// <-time.After — no flaky real-time sleeps, no race on timer settling.
func TestAllowlistDebouncer_CoalescesBurst(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		const window = 100 * time.Millisecond
		deb := newAllowlistDebouncer(window)
		defer deb.Stop()

		// Burst of triggers, each well within the window. Advancing a
		// fraction of the window between triggers proves the timer keeps
		// resetting (latest-event-wins) rather than firing per trigger.
		for range 5 {
			deb.Trigger()
			<-time.After(window / 10)
			synctest.Wait()
		}

		// Still inside the window after the last trigger: must not have
		// fired yet.
		if got := drainDebouncer(deb); got != 0 {
			t.Fatalf("debouncer fired %d times mid-burst, want 0 (not yet elapsed)", got)
		}

		// Cross the window from the last trigger → exactly one fire.
		<-time.After(window)
		synctest.Wait()

		if got := drainDebouncer(deb); got != 1 {
			t.Fatalf("debouncer fired %d times after window, want exactly 1 (coalesced)", got)
		}
	})
}

// TestAllowlistDebouncer_SeparatedTriggersFireSeparately is the
// complement: two triggers separated by MORE than the window each produce
// their own fire (the debouncer is not stuck after the first fire).
func TestAllowlistDebouncer_SeparatedTriggersFireSeparately(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		const window = 100 * time.Millisecond
		deb := newAllowlistDebouncer(window)
		defer deb.Stop()

		deb.Trigger()
		<-time.After(2 * window)
		synctest.Wait()
		if got := drainDebouncer(deb); got != 1 {
			t.Fatalf("first trigger fired %d times, want 1", got)
		}

		deb.Trigger()
		<-time.After(2 * window)
		synctest.Wait()
		if got := drainDebouncer(deb); got != 1 {
			t.Fatalf("second trigger fired %d times, want 1", got)
		}
	})
}

// TestAllowlistDebouncer_StopIdempotent asserts Stop is safe to call
// repeatedly (including before any Trigger) and drains a pending fire so
// a deferred Stop on watchAllowlist exit cannot leak the timer.
func TestAllowlistDebouncer_StopIdempotent(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		const window = 100 * time.Millisecond
		deb := newAllowlistDebouncer(window)

		deb.Stop() // before any trigger
		deb.Stop() // idempotent

		// Arm then stop after the timer has fired — Stop must drain it.
		deb.Trigger()
		<-time.After(2 * window)
		synctest.Wait()
		deb.Stop()

		if got := drainDebouncer(deb); got != 0 {
			t.Fatalf("after Stop, C() still holds %d pending fires, want 0", got)
		}
	})
}

// waitForAllowlistSize polls al.Size() until it reaches want or the
// deadline elapses. Returns the final observed size. The poll uses
// <-time.After ticks (not time.Sleep) so it stays off the synctest
// migration inventory; the watcher under test drives a REAL fsnotify
// watch, so a bounded real-time wait is unavoidable but deterministic in
// outcome (either the reload landed or the test fails with the size).
func waitForAllowlistSize(al *domain.Allowlist, mu *sync.RWMutex, want int, deadline time.Duration) int {
	end := time.Now().Add(deadline)
	for {
		mu.RLock()
		got := al.Size()
		mu.RUnlock()
		if got == want || time.Now().After(end) {
			return got
		}
		<-time.After(5 * time.Millisecond)
	}
}

// TestWatchAllowlist_ReloadsOnRename exercises watchAllowlist end-to-end
// against a real fsnotify watch: it writes a new allowlist via the
// atomic write-then-rename path (the editor rename-on-save pattern the
// watcher is built for, see saveAllowlist) and asserts the in-memory
// allowlist picks up the new entry. Synchronisation is channel-based for
// startup/shutdown; the reload landing is observed by polling the
// allowlist size with a bounded deadline (real fsnotify latency).
func TestWatchAllowlist_ReloadsOnRename(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "allowlist.toml")

	// Seed with one entry so the watcher has a valid starting state.
	seed := domain.NewAllowlist()
	seed.Grant(mustParseJID(t, "15551234567@s.whatsapp.net"), domain.ActionSend)
	if err := saveAllowlist(path, seed); err != nil {
		t.Fatalf("seed save: %v", err)
	}

	al, err := loadAllowlist(path)
	if err != nil {
		t.Fatalf("load seed: %v", err)
	}
	if al.Size() != 1 {
		t.Fatalf("seed size = %d, want 1", al.Size())
	}

	var mu sync.RWMutex
	ctx, cancel := context.WithCancel(context.Background())
	log := discardLogger()

	watchErr := make(chan error, 1)
	go func() {
		watchErr <- watchAllowlist(ctx, path, al, &mu, nopAudit{}, log)
	}()
	t.Cleanup(func() {
		cancel()
		select {
		case err := <-watchErr:
			if err != nil {
				t.Errorf("watchAllowlist returned error: %v", err)
			}
		case <-time.After(2 * time.Second):
			t.Error("watchAllowlist did not return after ctx cancel")
		}
	})

	// fsnotify needs the directory watch registered before the write. The
	// watcher registers it synchronously before its select loop, but the
	// goroutine scheduling is not observable; a short bounded settle here
	// (channel timer, not time.Sleep) avoids racing the watcher startup.
	<-time.After(50 * time.Millisecond)

	// Write the updated allowlist via the atomic rename path.
	updated := domain.NewAllowlist()
	updated.Grant(mustParseJID(t, "15551234567@s.whatsapp.net"), domain.ActionSend, domain.ActionRead)
	updated.Grant(mustParseJID(t, "15559998888@s.whatsapp.net"), domain.ActionSend)
	if err := saveAllowlist(path, updated); err != nil {
		t.Fatalf("updated save: %v", err)
	}

	// Reload coalesces through the 100ms debouncer; allow generous real
	// time for the fsnotify event + debounce + reload to land.
	if got := waitForAllowlistSize(al, &mu, 2, 3*time.Second); got != 2 {
		t.Fatalf("after rename, allowlist size = %d, want 2 (reload did not fire)", got)
	}

	// The new JID must be present with its granted action.
	mu.RLock()
	defer mu.RUnlock()
	if !al.Allows(mustParseJID(t, "15559998888@s.whatsapp.net"), domain.ActionSend) {
		t.Error("reloaded allowlist missing newly-granted JID/action")
	}
}
