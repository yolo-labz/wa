package whatsmeow

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"go.uber.org/goleak"

	"github.com/yolo-labz/wa/v2/internal/adapters/secondary/sqlitehistory"
	"github.com/yolo-labz/wa/v2/internal/domain"
)

var (
	gapChatA = domain.MustJID("15550000001@s.whatsapp.net")
	gapChatB = domain.MustJID("15550000002@s.whatsapp.net")
	gapChatC = domain.MustJID("15550000003@s.whatsapp.net")
)

// fakeEvidence is the in-memory recovery peer: episodes whose anchors name
// rows the fake history actually holds.
type fakeEvidence struct {
	mu  sync.Mutex
	eps []domain.RecoveryEpisode
}

func (f *fakeEvidence) RecoveryEpisodes() []domain.RecoveryEpisode {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]domain.RecoveryEpisode(nil), f.eps...)
}

// probe is a non-blocking acquire: admitted only when a slot is free now and
// nobody is queued ahead.
func probe(b *pullBudget, chat string) (func(), bool) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	rel, err := b.acquire(ctx, chat)
	return rel, err == nil
}

type pullRecord struct {
	chat, result string
	gen          uint64
	received     int
}

type pullLog struct {
	mu   sync.Mutex
	recs []pullRecord
}

func (p *pullLog) record(_ context.Context, chat string, gen uint64, result string, received int) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.recs = append(p.recs, pullRecord{chat, result, gen, received})
	return nil
}

func (p *pullLog) all() []pullRecord {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]pullRecord(nil), p.recs...)
}

func queued(b *pullBudget) []string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([]string(nil), b.queue...)
}

func (f *fakeWhatsmeowClient) builds() []recordedBuildHS {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]recordedBuildHS(nil), f.BuildHSReqs...)
}

// preOutage is the chat's newest stored row from before the outage.
func preOutage(chat domain.JID) domain.MessageRef {
	return domain.MessageRef{Chat: chat, ID: "pre-outage", Timestamp: time.Unix(1_700_000_000, 0)}
}

func liveEpisode(chat domain.JID, gen uint64, anchor string) domain.RecoveryEpisode {
	return domain.RecoveryEpisode{ChatJID: chat.String(), Generation: gen, Pending: true, AnchorID: anchor, AnchorTS: 1_700_000_100}
}

func gapAdapter(t *testing.T, ev *fakeEvidence, chats ...domain.JID) (*Adapter, *fakeWhatsmeowClient) {
	t.Helper()
	fc := newFakeClient()
	fc.ConnectedFlag, fc.LoggedInFlag = true, true
	a := newTestAdapter(t, fc)
	h := &syncHistory{newest: map[string]domain.MessageRef{}, chats: chats}
	for _, c := range chats {
		h.newest[c.String()] = preOutage(c)
	}
	a.history = h
	a.SetRecoveryEvidence(ev)
	return a, fc
}

func closeAdapter(t *testing.T, a *Adapter) {
	t.Helper()
	t.Cleanup(func() { _ = a.Close() })
}

func TestForceSync_TailGapUnreachable(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		missing := domain.MustJID("15550000009@s.whatsapp.net")
		ev := &fakeEvidence{eps: []domain.RecoveryEpisode{liveEpisode(gapChatA, 1, ""), liveEpisode(missing, 1, "")}}
		a, fc := gapAdapter(t, ev, gapChatA, gapChatC)
		closeAdapter(t, a)
		for _, tc := range []struct {
			chat   domain.JID
			reason string
		}{{gapChatA, ReasonTailGapUnanchored}, {missing, ReasonNoAnchor}} {
			r, err := a.ForceSync(t.Context(), tc.chat, 0, false)
			if err != nil || r.Requested || r.GapReachable == nil || *r.GapReachable || r.Reason != tc.reason {
				t.Fatalf("%s: want unreachable %s and no pull; got %+v err=%v", tc.chat, tc.reason, r, err)
			}
		}
		if n := len(fc.builds()); n != 0 {
			t.Fatalf("a tail gap issued %d network pulls", n)
		}
	})
}

// The anchor is the message a real store committed live after recovery, not
// the pre-outage newest row; an ACK never clears the episode.
func TestForceSync_RealPostRecoveryAnchor(t *testing.T) {
	db := filepath.Join(t.TempDir(), "m.db")
	store, err := sqlitehistory.Open(t.Context(), db)
	if err != nil {
		t.Fatal(err)
	}
	pre := domain.HistoryRecord{ChatJID: gapChatA.String(), SenderJID: gapChatA.String(), MessageID: "pre-outage", Timestamp: 1_700_000_000, Body: "x"}
	if err = store.InsertLive(t.Context(), pre, time.Now()); err != nil {
		t.Fatal(err)
	}
	if err = store.Close(); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(db+".recovery", []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}
	if store, err = sqlitehistory.Open(t.Context(), db); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	live := pre
	live.MessageID, live.Timestamp = "live-1", 1_700_000_500
	if err = store.InsertLive(t.Context(), live, time.Now()); err != nil {
		t.Fatal(err)
	}

	a, fc := gapAdapter(t, &fakeEvidence{}, gapChatA)
	closeAdapter(t, a)
	a.SetRecoveryEvidence(store)
	a.forceSyncTimeout = 2 * time.Second
	done := make(chan ForceSyncResult, 1)
	go func() { r, _ := a.ForceSync(context.Background(), gapChatA, 0, false); done <- r }()
	if !waitForPending(a, 1, time.Second) || !a.resolveHistoryReq([]domain.Message{domain.TextMessage{Recipient: gapChatA, Body: "old"}}) {
		t.Fatal("pull never registered")
	}
	r := <-done
	b := fc.builds()
	if len(b) != 1 || b[0].LastKnown.ID != "live-1" || b[0].Count != historyRoundTripCap {
		t.Fatalf("want one capped pull before live-1; got %+v", b)
	}
	if r.GapReachable == nil || !*r.GapReachable || r.Reason != ReasonPostRecoveryAnchor || !r.Delivered || r.Received != 1 {
		t.Fatalf("result: %+v", r)
	}
	if eps := store.RecoveryEpisodes(); len(eps) != 1 || !eps[0].Pending {
		t.Fatalf("an ACK cleared the gap: %+v", eps)
	}
}

func TestForceSync_GlobalReportsPerChatReachability(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ev := &fakeEvidence{eps: []domain.RecoveryEpisode{liveEpisode(gapChatA, 1, "live-a"), liveEpisode(gapChatB, 1, "")}}
		a, fc := gapAdapter(t, ev, gapChatA, gapChatB, gapChatC)
		closeAdapter(t, a)
		r, err := a.ForceSync(t.Context(), domain.JID{}, 0, false)
		if err != nil || !r.Async || len(r.Chats) != 3 {
			t.Fatalf("sweep: %+v err=%v", r, err)
		}
		want := map[string]string{gapChatA.String(): ReasonPostRecoveryAnchor, gapChatB.String(): ReasonTailGapUnanchored, gapChatC.String(): ReasonNoPendingGap}
		for _, c := range r.Chats {
			if c.Reason != want[c.Chat] || c.Requested == (c.Chat == gapChatB.String()) {
				t.Errorf("chat %s: %+v", c.Chat, c)
			}
		}
		if c := r.Chats[0]; c.GapReachable == nil || !*c.GapReachable {
			t.Errorf("anchored chat not reachable: %+v", c)
		}
		ids := map[string]bool{}
		for _, b := range fc.builds() {
			ids[b.LastKnown.ID] = true
		}
		if len(ids) != 2 || !ids["live-a"] || !ids["pre-outage"] {
			t.Fatalf("pull anchors: %v", ids)
		}
	})
}

func TestForceSync_ExplicitOlderHistoryFlag(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		a, fc := gapAdapter(t, &fakeEvidence{eps: []domain.RecoveryEpisode{liveEpisode(gapChatA, 1, "")}}, gapChatA)
		closeAdapter(t, a)
		r, err := a.ForceSync(t.Context(), gapChatA, 0, true)
		if err != nil || !r.Requested || !r.TimedOut || r.GapReachable == nil || *r.GapReachable || r.Reason != ReasonTailGapUnanchored {
			t.Fatalf("older_history must pull but keep false reachability: %+v err=%v", r, err)
		}
		if b := fc.builds(); len(b) != 1 || b[0].LastKnown.ID != "pre-outage" {
			t.Fatalf("pulls: %+v", b)
		}
	})
}

func TestDeferredBackfill_OptInEdgeAndCooldown(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		a, fc := gapAdapter(t, &fakeEvidence{}, gapChatA)
		closeAdapter(t, a)
		var log pullLog
		off := NewRecoveryBackfill(a, log.record, false)
		off.OnAnchor(liveEpisode(gapChatA, 1, "live-1"))
		synctest.Wait()
		off.Close()
		if len(fc.builds()) != 0 {
			t.Fatal("detect-only mode sent a pull")
		}

		w := NewRecoveryBackfill(a, log.record, true)
		defer w.Close()
		w.OnAnchor(liveEpisode(gapChatA, 1, "live-1"))
		w.OnAnchor(liveEpisode(gapChatA, 1, "live-1")) // same edge, no second pull
		synctest.Wait()
		if !a.resolveHistoryReq([]domain.Message{domain.TextMessage{Recipient: gapChatA, Body: "old"}}) {
			t.Fatal("no pull in flight")
		}
		synctest.Wait()
		if got := log.all(); len(got) != 1 || got[0] != (pullRecord{gapChatA.String(), "delivered", 1, 1}) || len(fc.builds()) != 1 {
			t.Fatalf("edge: records %+v pulls %d", got, len(fc.builds()))
		}

		w.OnAnchor(liveEpisode(gapChatA, 2, "live-2")) // new episode inside the cooldown
		<-time.After(recoveryCooldown - time.Second)
		synctest.Wait()
		if n := len(fc.builds()); n != 1 {
			t.Fatalf("cooldown breached: %d pulls", n)
		}
		<-time.After(2 * time.Second)
		synctest.Wait()
		if b := fc.builds(); len(b) != 2 || b[1].LastKnown.ID != "live-2" {
			t.Fatalf("queued pull did not run after cooldown: %+v", b)
		}
	})
}

func TestDeferredBackfill_CappedAt50(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		a, fc := gapAdapter(t, &fakeEvidence{}, gapChatA)
		closeAdapter(t, a)
		var log pullLog
		w := NewRecoveryBackfill(a, log.record, true)
		w.OnAnchor(liveEpisode(gapChatA, 1, "live-1"))
		<-time.After(historyRequestTimeout + time.Second)
		synctest.Wait()
		w.Close()
		if b := fc.builds(); len(b) != 1 || b[0].Count != 50 {
			t.Fatalf("want one 50-message pull; got %+v", b)
		}
		if got := log.all(); len(got) != 1 || got[0].result != "timed_out" {
			t.Fatalf("a capped, unanswered pull must stay visible as timed_out: %+v", got)
		}
	})
}

func TestDeferredBackfill_WorkerNoLeak(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		a, _ := gapAdapter(t, &fakeEvidence{}, gapChatA, gapChatB)
		t.Cleanup(func() {
			_ = a.Close()
			goleak.VerifyNone(t, leakFreeGoleakOptions()...)
		})
		var log pullLog
		w := NewRecoveryBackfill(a, log.record, true)
		w.OnAnchor(liveEpisode(gapChatA, 1, "live-a")) // in flight
		synctest.Wait()
		w.mu.Lock()
		w.last[gapChatB.String()] = time.Now()
		w.mu.Unlock()
		w.OnAnchor(liveEpisode(gapChatB, 1, "live-b")) // waiting out its cooldown
		synctest.Wait()
		w.Close()
		w.OnAnchor(liveEpisode(gapChatA, 2, "late")) // after Close: ignored
		if got := log.all(); len(got) != 0 {
			t.Fatalf("shutdown fabricated outcomes: %+v", got)
		}
	})
}

func TestDeferredBackfill_DaemonBudgetAndFairQueue(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var b pullBudget
		ctx := t.Context()
		r1, _ := b.acquire(ctx, "a")
		r2, _ := b.acquire(ctx, "b")
		if _, ok := probe(&b, "x"); ok {
			t.Fatal("third concurrent pull admitted")
		}
		type admitted struct {
			chat string
			rel  func()
		}
		got := make(chan admitted, 2)
		for _, c := range []string{"c", "d"} {
			go func() {
				if rel, err := b.acquire(ctx, c); err == nil {
					got <- admitted{c, rel}
				}
			}()
			synctest.Wait()
		}
		if _, err := b.acquire(ctx, "c"); !errors.Is(err, errPullQueued) {
			t.Fatalf("a queued chat minted a second pull: %v", err)
		}
		if _, ok := probe(&b, "x"); ok {
			t.Fatal("a newcomer jumped the fair queue")
		}
		r1()
		synctest.Wait()
		first := <-got
		if q := queued(&b); first.chat != "c" || len(q) != 1 || q[0] != "d" {
			t.Fatalf("queue not FIFO: admitted %q, queue %v", first.chat, q)
		}
		r2()
		second := <-got
		first.rel()
		second.rel()

		// 4 starts so far; 6 more fill the rolling minute.
		for i := range 6 {
			rel, ok := probe(&b, strconv.Itoa(i))
			if !ok {
				t.Fatalf("start %d refused under the 10/min budget", i+5)
			}
			rel()
		}
		if _, ok := probe(&b, "x"); ok {
			t.Fatal("11th start inside one minute admitted")
		}
		start := time.Now()
		rel, err := b.acquire(ctx, "late")
		if err != nil || time.Since(start) != time.Minute {
			t.Fatalf("waiter not admitted when the window rolled: %v after %s", err, time.Since(start))
		}
		rel()

		var full pullBudget
		full.active = maxConcurrentPulls
		cctx, cancel := context.WithCancel(ctx)
		for i := range maxPullQueue {
			go func() { _, _ = full.acquire(cctx, strconv.Itoa(i)) }()
		}
		synctest.Wait()
		if _, err := full.acquire(ctx, "overflow"); !errors.Is(err, errPullBudget) {
			t.Fatalf("queue cap not enforced: %v", err)
		}
		cancel()
		synctest.Wait()
		if q := queued(&full); len(q) != 0 {
			t.Fatalf("cancelled waiters left the queue dirty: %v", q)
		}
	})
}

// Review F1: one chat's unknown episode never poisons a chat without one; an
// unknown, unanchored chat is still never pulled from its pre-outage row.
func TestForceSync_UnknownDoesNotPoisonUntrackedChats(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		unknown := liveEpisode(gapChatA, 1, "")
		unknown.Unknown = true
		a, fc := gapAdapter(t, &fakeEvidence{eps: []domain.RecoveryEpisode{unknown}}, gapChatA, gapChatB)
		closeAdapter(t, a)
		if r, err := a.ForceSync(t.Context(), gapChatA, 0, false); err != nil || r.Requested || r.Reason != ReasonGapStateUnknown || *r.GapReachable {
			t.Fatalf("unknown chat: %+v err=%v", r, err)
		}
		r, err := a.ForceSync(t.Context(), gapChatB, 0, false)
		if err != nil || !r.Requested || r.Reason != ReasonNoPendingGap || r.GapReachable != nil {
			t.Fatalf("untracked chat poisoned by another chat's unknown episode: %+v err=%v", r, err)
		}
		if b := fc.builds(); len(b) != 1 || b[0].LastKnown.ID != "pre-outage" || b[0].LastKnown.Chat.User != gapChatB.User() {
			t.Fatalf("pulls: %+v", b)
		}
	})
}

func sweepChats(n int) []domain.JID {
	chats := make([]domain.JID, n)
	for i := range chats {
		chats[i] = domain.MustJID("1555000" + strconv.Itoa(1000+i) + "@s.whatsapp.net")
	}
	return chats
}

// Review F2: a sweep waits its FIFO turn instead of dropping chats; 12 chats
// over the 10/min budget start 10 at once and 2 when the window rolls.
func TestForceSync_GlobalSweepQueuesWithinBudget(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		a, fc := gapAdapter(t, &fakeEvidence{}, sweepChats(12)...)
		closeAdapter(t, a)
		start := time.Now()
		done := make(chan ForceSyncResult, 1)
		go func() { r, _ := a.ForceSync(t.Context(), domain.JID{}, 0, false); done <- r }()
		synctest.Wait()
		if n := len(fc.builds()); n != maxPullStartsPerMin {
			t.Fatalf("first window started %d pulls", n)
		}
		r := <-done
		if requested, withheld := r.SweepCounts(); requested != 12 || withheld != 0 || time.Since(start) != time.Minute {
			t.Fatalf("sweep: requested=%d withheld=%d after %s", requested, withheld, time.Since(start))
		}
		// A caller deadline inside a fresh window reports the rest as exhausted.
		<-time.After(time.Minute)
		ctx, cancel := context.WithTimeout(t.Context(), time.Second)
		defer cancel()
		r, _ = a.ForceSync(ctx, domain.JID{}, 0, false)
		if requested, withheld := r.SweepCounts(); requested != 10 || withheld != 2 {
			t.Fatalf("bounded sweep: requested=%d withheld=%d", requested, withheld)
		}
	})
}

// Review F2: one queued recovery pull no longer blanks the whole sweep; it
// keeps its FIFO place and the sweep follows.
func TestForceSync_SweepNotBlankedByQueuedRecovery(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		a, _ := gapAdapter(t, &fakeEvidence{}, gapChatA, gapChatB, gapChatC)
		closeAdapter(t, a)
		r1, _ := a.pulls.acquire(t.Context(), "busy-1")
		r2, _ := a.pulls.acquire(t.Context(), "busy-2")
		order := make(chan string, 4)
		go func() {
			if rel, err := a.pulls.acquire(t.Context(), "recovery"); err == nil {
				order <- "recovery"
				rel()
			}
		}()
		synctest.Wait()
		done := make(chan ForceSyncResult, 1)
		go func() { r, _ := a.ForceSync(t.Context(), domain.JID{}, 0, false); done <- r }()
		synctest.Wait()
		r1()
		r2()
		r := <-done
		order <- "sweep"
		if requested, withheld := r.SweepCounts(); requested != 3 || withheld != 0 {
			t.Fatalf("sweep blanked: %+v", r.Chats)
		}
		if first := <-order; first != "recovery" {
			t.Fatalf("sweep jumped the queued recovery pull")
		}
	})
}

// Reachability is ForceSync's verdict without its pull: the same reasons
// per chat and for a sweep, no network request, and no verdict when no gap
// is tracked.
func TestReachability_MatchesForceSyncWithoutPulling(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ev := &fakeEvidence{eps: []domain.RecoveryEpisode{liveEpisode(gapChatA, 1, "live-a"), liveEpisode(gapChatB, 1, "")}}
		a, fc := gapAdapter(t, ev, gapChatA, gapChatB, gapChatC)
		closeAdapter(t, a)
		for chat, want := range map[domain.JID]struct {
			reach  bool
			reason string
		}{gapChatA: {true, ReasonPostRecoveryAnchor}, gapChatB: {false, ReasonTailGapUnanchored}} {
			r := a.Reachability(t.Context(), chat, false)
			if r.GapReachable == nil || *r.GapReachable != want.reach || r.Reason != want.reason {
				t.Errorf("%s: %+v, want reachable=%v %s", chat, r, want.reach, want.reason)
			}
		}
		if r := a.Reachability(t.Context(), gapChatC, false); r.GapReachable != nil || r.Reason != ReasonNoPendingGap {
			t.Errorf("a chat with no episode must carry no verdict: %+v", r)
		}
		sweep := a.Reachability(t.Context(), domain.JID{}, false)
		if sweep.GapReachable != nil || len(sweep.Chats) != 3 {
			t.Errorf("sweep = %+v, want 3 per-chat verdicts and no single one", sweep)
		}
		if n := len(fc.builds()); n != 0 {
			t.Fatalf("Reachability issued %d network pulls", n)
		}
		// No store behind the adapter: undetermined is false, not optimistic.
		a.history = nil
		if r := a.Reachability(t.Context(), gapChatB, false); r.GapReachable == nil || *r.GapReachable {
			t.Errorf("no history: %+v, want false", r)
		}
	})
}
