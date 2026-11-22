package whatsmeow

import (
	"context"
	"errors"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/yolo-labz/wa/v2/internal/app"
	"github.com/yolo-labz/wa/v2/internal/domain"
)

func asCaller(id string) context.Context {
	return app.WithCaller(context.Background(), app.TokenPrincipal(id))
}

func isCallerBudget(err error) (*callerBudgetError, bool) {
	var cb *callerBudgetError
	return cb, errors.As(err, &cb)
}

// A greedy token cannot take a second concurrent pull, a second token still
// can, and the slot comes back on release.
func TestPullBudget_GreedyCallerCannotStarveAnother(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var b pullBudget
		a1, err := b.acquire(asCaller("a"), "chat-1")
		if err != nil {
			t.Fatal(err)
		}
		if _, err := b.acquire(asCaller("a"), "chat-2"); err == nil {
			t.Fatal("a took a second concurrent pull")
		} else if cb, ok := isCallerBudget(err); !ok || cb.RetryAfter != 0 {
			t.Fatalf("second pull = %v, want a callerBudgetError with no invented retry time", err)
		}
		if _, err := b.acquire(asCaller("b"), "chat-3"); err != nil {
			t.Fatalf("a greedy caller starved b: %v", err)
		}
		a1()
		if _, err := b.acquire(asCaller("a"), "chat-2"); err != nil {
			t.Fatalf("a's slot did not come back on release: %v", err)
		}
	})
}

// Starts per minute are per caller: a's 5th inside the window is refused with
// the time until its oldest start leaves, b is unaffected, and a recovers then.
func TestPullBudget_CallerStartsPerMinute(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var b pullBudget
		for i := range maxCallerPullStartsPerMin {
			rel, err := b.acquire(asCaller("a"), string(rune('A'+i)))
			if err != nil {
				t.Fatalf("start %d: %v", i+1, err)
			}
			rel()
			<-time.After(time.Second)
		}
		_, err := b.acquire(asCaller("a"), "late")
		cb, ok := isCallerBudget(err)
		want := time.Minute - maxCallerPullStartsPerMin*time.Second
		if !ok || cb.RetryAfter != want {
			t.Fatalf("5th start = %v, want retry in %s", err, want)
		}
		if rel, err := b.acquire(asCaller("b"), "other"); err != nil {
			t.Fatalf("b refused for a's starts: %v", err)
		} else {
			rel()
		}
		<-time.After(cb.RetryAfter)
		if _, err := b.acquire(asCaller("a"), "late"); err != nil {
			t.Fatalf("a still refused after the hinted wait: %v", err)
		}
	})
}

// The daemon-wide cap stays: two callers fill it and a third waits its turn.
// Callers with no principal or an Admin one stay on the daemon pool only.
func TestPullBudget_DaemonCapAndUnkeyedCallers(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var b pullBudget
		ra, _ := b.acquire(asCaller("a"), "c1")
		rb, _ := b.acquire(asCaller("b"), "c2")
		got := make(chan error, 1)
		go func() {
			rel, err := b.acquire(asCaller("c"), "c3")
			if err == nil {
				rel()
			}
			got <- err
		}()
		synctest.Wait()
		select {
		case err := <-got:
			t.Fatalf("a third pull ran past the daemon cap of %d: %v", maxConcurrentPulls, err)
		default:
		}
		ra()
		if err := <-got; err != nil {
			t.Fatalf("c after a slot freed: %v", err)
		}
		rb()
		for _, ctx := range []context.Context{context.Background(), app.WithCaller(context.Background(), app.SocketPrincipal("p"))} {
			for i := range maxCallerPullStartsPerMin + 2 {
				rel, err := b.acquire(ctx, string(rune('A'+i)))
				if err != nil {
					t.Fatalf("an unkeyed caller was sub-budgeted at pull %d: %v", i+1, err)
				}
				rel()
			}
			<-time.After(time.Minute)
		}
	})
}

// A caller whose acquire is cancelled while waiting gets its slot back, and a
// stale caller entry is forgotten once its window has passed.
func TestPullBudget_CancelReturnsCallerSlotAndStateIsPruned(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var b pullBudget
		r1, _ := b.acquire(context.Background(), "x1")
		r2, _ := b.acquire(context.Background(), "x2")
		ctx, cancel := context.WithCancel(asCaller("a"))
		done := make(chan error, 1)
		go func() { _, err := b.acquire(ctx, "c1"); done <- err }()
		synctest.Wait()
		cancel()
		if err := <-done; !errors.Is(err, context.Canceled) {
			t.Fatalf("cancelled acquire = %v", err)
		}
		r1()
		r2()
		if rel, err := b.acquire(asCaller("a"), "c2"); err != nil {
			t.Fatalf("the cancelled waiter kept a's slot: %v", err)
		} else {
			rel()
		}
		<-time.After(time.Minute + time.Second)
		rel, _ := b.acquire(asCaller("z"), "c3")
		rel()
		if n := len(b.callers); n != 1 {
			t.Fatalf("%d caller entries after the window passed, want only z", n)
		}
	})
}

// sync.force surface: a token's sweep gets its own share only and reports the
// rest as caller_exhausted with a retry hint; a second token's sweep still pulls.
func TestForceSync_SweepReportsCallerShare(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		a, _ := gapAdapter(t, &fakeEvidence{}, sweepChats(6)...)
		closeAdapter(t, a)
		r, _ := a.ForceSync(asCaller("a"), domain.JID{}, 0, false)
		requested, withheld := r.SweepCounts()
		if requested != 4 || withheld != 2 {
			t.Fatalf("token a: requested=%d withheld=%d", requested, withheld)
		}
		for _, c := range r.Chats {
			if !c.Requested && (c.Budget != "caller_exhausted" || c.RetryAfter != 60) {
				t.Fatalf("withheld chat %+v, want caller_exhausted with a 60 s hint", c)
			}
		}
		if r2, _ := a.ForceSync(asCaller("b"), domain.JID{}, 0, false); !r2.Requested {
			t.Fatalf("token b starved by a: %+v", r2.Chats)
		}
	})
}

// A single-chat force reports the same refusal; the hint is rounded up to
// whole seconds (4 pulls of 400 ms leave 58.4 s of the window).
func TestForceSync_ChatReportsCallerShare(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ev := &fakeEvidence{eps: []domain.RecoveryEpisode{liveEpisode(gapChatA, 1, "live-a")}}
		a, _ := gapAdapter(t, ev, gapChatA)
		closeAdapter(t, a)
		a.forceSyncTimeout = 400 * time.Millisecond
		for i := range 4 {
			if r, _ := a.ForceSync(asCaller("a"), gapChatA, 0, false); !r.Requested {
				t.Fatalf("pull %d inside the share: %+v", i+1, r)
			}
		}
		r, err := a.ForceSync(asCaller("a"), gapChatA, 0, false)
		if err != nil || r.Requested || r.Budget != "caller_exhausted" || r.RetryAfterSec != 59 {
			t.Fatalf("5th pull = %+v, %v; want caller_exhausted, retry 59 s", r, err)
		}
	})
}

// WA-SYNC-SUBBUDGET-FIX L1: only a pull that starts counts toward the minute.
// Attempts cut off while queued behind the daemon pool burned it before.
func TestPullBudget_CancelledAttemptsDoNotBurnTheMinute(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var b pullBudget
		r1, _ := b.acquire(context.Background(), "u1")
		r2, _ := b.acquire(context.Background(), "u2") // daemon pool full
		defer r2()
		for i := range 2 * maxCallerPullStartsPerMin {
			ctx, cancel := context.WithTimeout(asCaller("a"), time.Second)
			if _, err := b.acquire(ctx, "c"+strconv.Itoa(i)); !errors.Is(err, context.DeadlineExceeded) {
				t.Fatalf("attempt %d: %v, want its own timeout", i, err)
			}
			cancel()
		}
		r1()
		rel, err := b.acquire(asCaller("a"), "real")
		if err != nil {
			t.Fatalf("a refused for %d attempts that never pulled: %v", 2*maxCallerPullStartsPerMin, err)
		}
		rel()
	})
}

// L2: a busy refusal promises no retry time; the sweep and chat results omit it.
func TestCallerBudgetError_BusyHasNoHint(t *testing.T) {
	if msg := (&callerBudgetError{}).Error(); !strings.Contains(msg, "still running") {
		t.Fatalf("busy message %q promises a time", msg)
	}
	if got := retrySeconds(&callerBudgetError{}); got != 0 {
		t.Fatalf("busy hint = %d s, want none", got)
	}
	if got := retrySeconds(&callerBudgetError{RetryAfter: 1500 * time.Millisecond}); got != 2 {
		t.Fatalf("1.5 s rounds to %d, want 2", got)
	}
}

// L3: the sweep's send gives its slot back however it ends.
func TestSendAndRelease_ReleasesOnce(t *testing.T) {
	var a Adapter
	n := 0
	a.sendAndRelease(context.Background(), func() { n++ }, nil, 10)
	if n != 1 {
		t.Fatalf("release ran %d times, want 1", n)
	}
}

// L3 end to end: a sweep whose send unwinds without returning (Goexit is not
// recovered by sendHistoryRequest) still gives the slot back, so the token
// is not locked out for good.
func TestForceSync_SweepUnwindReleasesTheCallerSlot(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		a, fc := gapAdapter(t, &fakeEvidence{}, sweepChats(2)...)
		closeAdapter(t, a)
		fc.OnBuildHS = runtime.Goexit
		go func() { _, _ = a.ForceSync(asCaller("a"), domain.JID{}, 0, false) }()
		synctest.Wait()
		a.pulls.mu.Lock()
		defer a.pulls.mu.Unlock()
		if c := a.pulls.callers["token:a"]; a.pulls.active != 0 || (c != nil && c.active != 0) {
			t.Fatalf("slot leaked: daemon active %d, caller %+v", a.pulls.active, c)
		}
	})
}
