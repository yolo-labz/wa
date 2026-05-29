package main

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/yolo-labz/wa/v2/internal/adapters/secondary/sqlitecontacts"
	"github.com/yolo-labz/wa/v2/internal/adapters/secondary/sqlitedrafts"
	"github.com/yolo-labz/wa/v2/internal/adapters/secondary/sqliteevents"
	"github.com/yolo-labz/wa/v2/internal/adapters/secondary/sqliteschedule"
)

// discardLogger is defined in health_http_test.go (same package) and
// returns a slog.Logger backed by slog.DiscardHandler. Reused here so the
// cleanup helpers' unhappy-path logging stays out of CI output.

// fakeCloser is an injectable interface{ Close() error } used to drive
// closeWithTimeout. It records whether Close ran, can return a canned
// error, and can block until released so the deadline path is testable
// without any time.Sleep.
type fakeCloser struct {
	mu     sync.Mutex
	closed bool

	err   error
	block <-chan struct{} // when non-nil, Close blocks on this before returning
}

func (f *fakeCloser) Close() error {
	if f.block != nil {
		<-f.block
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.closed = true
	return f.err
}

func (f *fakeCloser) wasClosed() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.closed
}

// --- closeWithTimeout: the FR-040 per-component guarantee ---

// TestCloseWithTimeout_InvokesClose asserts the happy path actually calls
// the component's Close.
func TestCloseWithTimeout_InvokesClose(t *testing.T) {
	t.Parallel()

	fc := &fakeCloser{}
	closeWithTimeout(discardLogger(), "ok", fc, time.Second)
	if !fc.wasClosed() {
		t.Error("closeWithTimeout did not invoke Close")
	}
}

// TestCloseWithTimeout_ErrorDoesNotPanic asserts a Close that returns an
// error is tolerated (logged, swallowed) — the guarantee the whole
// graceful-shutdown chain relies on so one failing Close cannot abort the
// rest.
func TestCloseWithTimeout_ErrorDoesNotPanic(t *testing.T) {
	t.Parallel()

	fc := &fakeCloser{err: errors.New("boom")}
	closeWithTimeout(discardLogger(), "errs", fc, time.Second)
	if !fc.wasClosed() {
		t.Error("closeWithTimeout did not invoke Close on the erroring component")
	}
}

// TestCloseWithTimeout_BoundedByDeadline asserts a Close that blocks past
// the timeout does NOT hang the caller — closeWithTimeout returns within
// the deadline and abandons the call. We give the blocked goroutine a
// generous real-time budget (well under the would-be-forever block) and
// fail if the helper itself overruns. No time.Sleep: the block is a
// channel never closed until cleanup.
func TestCloseWithTimeout_BoundedByDeadline(t *testing.T) {
	t.Parallel()

	release := make(chan struct{})
	t.Cleanup(func() { close(release) }) // unblock the abandoned goroutine at test end
	fc := &fakeCloser{block: release}

	const timeout = 50 * time.Millisecond
	returned := make(chan struct{})
	go func() {
		closeWithTimeout(discardLogger(), "blocker", fc, timeout)
		close(returned)
	}()

	select {
	case <-returned:
		// Good: returned despite the component still blocking.
	case <-time.After(2 * time.Second):
		t.Fatal("closeWithTimeout did not return within deadline; it hung on a blocking Close")
	}

	// The component must NOT have completed Close (still blocked).
	if fc.wasClosed() {
		t.Error("blocking component reported closed before release; timeout path not exercised")
	}
}

// --- closeBestEffort: typed-nil safety + both-close ---

// TestCloseBestEffort_NilSafe asserts closeBestEffort tolerates both
// typed-nil stores (the documented best-effort path where a store failed
// to open). The guarantee in main.go is that the nil check happens on the
// typed pointer, never via an interface wrapper.
func TestCloseBestEffort_NilSafe(t *testing.T) {
	t.Parallel()

	// Must not panic on typed-nil pointers.
	closeBestEffort(nil, nil)
}

// TestCloseBestEffort_ClosesRealStores opens real temp-dir events +
// contacts stores and asserts closeBestEffort closes them (a second Close
// returning without panicking is the observable signal they were closed).
func TestCloseBestEffort_ClosesRealStores(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	dir := t.TempDir()

	ev, err := sqliteevents.Open(ctx, dir+"/events.db", 100)
	if err != nil {
		t.Fatalf("open events: %v", err)
	}
	ct, err := sqlitecontacts.Open(ctx, dir+"/contacts.db")
	if err != nil {
		t.Fatalf("open contacts: %v", err)
	}

	closeBestEffort(ev, ct)

	// Idempotency / closed-state sanity: a second Close must not panic.
	_ = ev.Close()
	_ = ct.Close()
}

// --- gracefulShutdown ordering + non-abort guarantees ---

// TestGracefulShutdown_PhaseOrdering drives the real gracefulShutdown
// using only the injectable seams that span three of the four documented
// phases — HTTP (restShutdown), dispatch (bridgeCancel), watcher
// (watchCancel) — and asserts they fire in that order. Concrete-typed
// store fields are left nil (the chain skips nil fields), so this test
// isolates the cross-phase ordering contract from store internals.
func TestGracefulShutdown_PhaseOrdering(t *testing.T) {
	t.Parallel()

	var (
		mu     sync.Mutex
		events []string
	)
	record := func(name string) {
		mu.Lock()
		defer mu.Unlock()
		events = append(events, name)
	}

	watchDone := make(chan struct{})
	close(watchDone) // pre-closed so shutdownWatcher's <-watchDone returns immediately

	c := &startupCleanup{
		log:             discardLogger(),
		shutdownTimeout: time.Second,
		restShutdown: func(context.Context) error {
			record("http")
			return nil
		},
		bridgeCancel: func() { record("dispatch") },
		watchCancel:  func() { record("watcher") },
		watchDone:    watchDone,
	}

	c.gracefulShutdown()

	mu.Lock()
	defer mu.Unlock()
	want := []string{"http", "dispatch", "watcher"}
	if len(events) != len(want) {
		t.Fatalf("recorded %v, want %v", events, want)
	}
	for i := range want {
		if events[i] != want[i] {
			t.Fatalf("phase order = %v, want %v", events, want)
		}
	}
}

// TestGracefulShutdown_HTTPErrorDoesNotAbortChain asserts the
// chain-level graceful-shutdown semantics (FR-040): an HTTP-phase
// shutdown that returns an error must NOT short-circuit the dispatch and
// watcher phases.
func TestGracefulShutdown_HTTPErrorDoesNotAbortChain(t *testing.T) {
	t.Parallel()

	dispatchRan := false
	watcherRan := false

	watchDone := make(chan struct{})
	close(watchDone)

	c := &startupCleanup{
		log:             discardLogger(),
		shutdownTimeout: time.Second,
		restShutdown: func(context.Context) error {
			return errors.New("rest shutdown failed")
		},
		bridgeCancel: func() { dispatchRan = true },
		watchCancel:  func() { watcherRan = true },
		watchDone:    watchDone,
	}

	c.gracefulShutdown()

	if !dispatchRan {
		t.Error("dispatch phase did not run after HTTP-phase error (chain aborted)")
	}
	if !watcherRan {
		t.Error("watcher phase did not run after HTTP-phase error (chain aborted)")
	}
}

// TestGracefulShutdown_WaitsForWatcherDone asserts shutdownWatcher
// blocks on watchDone before the sequence proceeds — the
// watcher-goroutine join barrier. The dispatch phase signals it has run;
// we then assert gracefulShutdown does NOT return until watchDone is
// closed. No time.Sleep: synchronisation is purely channel-based.
func TestGracefulShutdown_WaitsForWatcherDone(t *testing.T) {
	t.Parallel()

	watchDone := make(chan struct{})
	watcherCancelled := make(chan struct{})
	finished := make(chan struct{})

	c := &startupCleanup{
		log:             discardLogger(),
		shutdownTimeout: time.Second,
		watchCancel:     func() { close(watcherCancelled) },
		watchDone:       watchDone,
	}

	go func() {
		c.gracefulShutdown()
		close(finished)
	}()

	// watchCancel must have been called (watcher signalled to stop)...
	select {
	case <-watcherCancelled:
	case <-time.After(2 * time.Second):
		t.Fatal("watchCancel was never invoked")
	}

	// ...but gracefulShutdown must still be blocked on watchDone.
	select {
	case <-finished:
		t.Fatal("gracefulShutdown returned before watchDone closed; barrier missing")
	case <-time.After(50 * time.Millisecond):
		// Expected: still blocked. The 50ms is an upper bound on "is it
		// blocked", not a race-timing sleep — release happens next.
	}

	close(watchDone)

	select {
	case <-finished:
		// Good: unblocked once the watcher goroutine joined.
	case <-time.After(2 * time.Second):
		t.Fatal("gracefulShutdown did not return after watchDone closed")
	}
}

// --- error-path run() chain over real stores ---

// TestStartupCleanupRun_ClosesRealStores exercises the error-path
// teardown (startupCleanup.run -> runStoresShutdown) over real temp-dir
// stores, asserting it closes every set store and is safe to call twice
// (the documented "safe to call multiple times" idempotency). Order
// across concrete stores is not observable, so this test asserts the
// no-panic / no-hang / idempotent guarantees the source documents.
func TestStartupCleanupRun_ClosesRealStores(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	dir := t.TempDir()

	sched, err := sqliteschedule.Open(ctx, dir+"/schedule.db")
	if err != nil {
		t.Fatalf("open schedule: %v", err)
	}
	draft, err := sqlitedrafts.Open(ctx, dir+"/drafts.db")
	if err != nil {
		t.Fatalf("open drafts: %v", err)
	}
	contacts, err := sqlitecontacts.Open(ctx, dir+"/contacts.db")
	if err != nil {
		t.Fatalf("open contacts: %v", err)
	}
	events, err := sqliteevents.Open(ctx, dir+"/events.db", 100)
	if err != nil {
		t.Fatalf("open events: %v", err)
	}

	watchDone := make(chan struct{})
	close(watchDone)

	cancelCalls := 0
	c := &startupCleanup{
		log:             discardLogger(),
		shutdownTimeout: time.Second,
		bridgeCancel:    func() { cancelCalls++ },
		watchCancel:     func() { cancelCalls++ },
		watchDone:       watchDone,
		scheduleStore:   sched,
		draftStore:      draft,
		contactsStore:   contacts,
		eventsStore:     events,
	}

	// First teardown.
	c.run()
	if cancelCalls != 2 {
		t.Errorf("cancel funcs invoked %d times, want 2", cancelCalls)
	}

	// Idempotency: a second run() must not panic. watchDone is already
	// closed (receive on a closed channel returns immediately), and the
	// stores tolerate a double Close.
	c.run()
}

// TestStartupCleanupRun_EmptyIsNoop asserts a zero-value cleanup (every
// field nil) tears down cleanly — the "safe to call mid-startup before
// every store is wired" invariant from the doc comment.
func TestStartupCleanupRun_EmptyIsNoop(t *testing.T) {
	t.Parallel()

	c := &startupCleanup{log: discardLogger(), shutdownTimeout: time.Second}
	c.run()              // error-path entry
	c.gracefulShutdown() // success-path entry
}
