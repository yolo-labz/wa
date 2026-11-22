package sqliteschedule_test

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/yolo-labz/wa/v2/internal/adapters/secondary/sqliteschedule"
	"github.com/yolo-labz/wa/v2/internal/app"
	"github.com/yolo-labz/wa/v2/internal/app/porttest"
	"github.com/yolo-labz/wa/v2/internal/domain"
)

// TestScheduledStoreContract runs the T1-18 standalone runner against
// the SQLite schedule adapter.
func TestScheduledStoreContract(t *testing.T) {
	porttest.RunScheduledStoreContract(t, func(t *testing.T) app.ScheduledStore {
		dbPath := filepath.Join(t.TempDir(), "scheduled.db")
		return openStore(t, dbPath)
	})
}

func fixedNow() time.Time { return time.Date(2026, 4, 20, 12, 0, 0, 0, time.UTC) }

func mustPending(t *testing.T, id string, fireIn time.Duration) domain.ScheduledSend {
	t.Helper()
	jid, err := domain.Parse("5511999999999")
	if err != nil {
		t.Fatalf("parse jid: %v", err)
	}
	ss, err := domain.NewScheduledSend(domain.ScheduledSendSpec{
		ID: id, Profile: "default", Kind: domain.ScheduleKindSendText, To: jid,
		Body: "hello", FireAt: fixedNow().Add(fireIn), Now: fixedNow(),
	})
	if err != nil {
		t.Fatalf("NewScheduledSend: %v", err)
	}
	return ss
}

func openStore(t *testing.T, dbPath string) *sqliteschedule.Store {
	t.Helper()
	s, err := sqliteschedule.Open(context.Background(), dbPath)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func TestScheduleStorePutGetListPendingDelete(t *testing.T) {
	t.Parallel()
	dbPath := filepath.Join(t.TempDir(), "scheduled.db")
	s := openStore(t, dbPath)
	ctx := context.Background()

	a := mustPending(t, "sch-a", time.Hour)
	b := mustPending(t, "sch-b", 30*time.Minute)

	if err := s.Put(ctx, a); err != nil {
		t.Fatalf("Put a: %v", err)
	}
	if err := s.Put(ctx, b); err != nil {
		t.Fatalf("Put b: %v", err)
	}

	got, err := s.Get(ctx, "default", "sch-a")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.ID() != "sch-a" || got.Body() != "hello" || got.State() != domain.SchedulePending {
		t.Fatalf("Get mismatch: %+v", got)
	}

	pending, err := s.ListPending(ctx, "default")
	if err != nil {
		t.Fatalf("ListPending: %v", err)
	}
	if len(pending) != 2 {
		t.Fatalf("ListPending len=%d want 2", len(pending))
	}
	// ordered by fire_at ASC: b (30m) before a (1h)
	if pending[0].ID() != "sch-b" || pending[1].ID() != "sch-a" {
		t.Fatalf("order wrong: %s then %s", pending[0].ID(), pending[1].ID())
	}

	if err := s.Delete(ctx, "default", "sch-a"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := s.Get(ctx, "default", "sch-a"); !errors.Is(err, sqliteschedule.ErrNotFound) {
		t.Fatalf("Get after Delete: want ErrNotFound, got %v", err)
	}
	if err := s.Delete(ctx, "default", "sch-a"); !errors.Is(err, sqliteschedule.ErrNotFound) {
		t.Fatalf("Delete missing: want ErrNotFound, got %v", err)
	}
}

func TestScheduleStoreUpdateTransitionsState(t *testing.T) {
	t.Parallel()
	dbPath := filepath.Join(t.TempDir(), "scheduled.db")
	s := openStore(t, dbPath)
	ctx := context.Background()

	ss := mustPending(t, "sch-1", time.Hour)
	if err := s.Put(ctx, ss); err != nil {
		t.Fatalf("Put: %v", err)
	}

	fired, err := ss.MarkFired(fixedNow().Add(time.Hour))
	if err != nil {
		t.Fatalf("MarkFired: %v", err)
	}
	if err := s.Update(ctx, fired); err != nil {
		t.Fatalf("Update: %v", err)
	}

	got, err := s.Get(ctx, "default", "sch-1")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.State() != domain.ScheduleFired {
		t.Fatalf("state=%v want fired", got.State())
	}

	// Update on missing row returns ErrNotFound.
	ghost := mustPending(t, "sch-ghost", time.Hour)
	if err := s.Update(ctx, ghost); !errors.Is(err, sqliteschedule.ErrNotFound) {
		t.Fatalf("Update missing: want ErrNotFound, got %v", err)
	}
}

// TestScheduleStoreRestartSurvives is the SC-08 durability check:
// after Close+Open, pending rows reload with state intact.
func TestScheduleStoreRestartSurvives(t *testing.T) {
	t.Parallel()
	dbPath := filepath.Join(t.TempDir(), "scheduled.db")

	// Boot 1: put two pending rows, fire one, cancel another preview.
	s1 := openStore(t, dbPath)
	ctx := context.Background()
	alive := mustPending(t, "alive", time.Hour)
	gone := mustPending(t, "gone", 2*time.Hour)
	if err := s1.Put(ctx, alive); err != nil {
		t.Fatalf("Put alive: %v", err)
	}
	if err := s1.Put(ctx, gone); err != nil {
		t.Fatalf("Put gone: %v", err)
	}
	cancelled, err := gone.MarkCancelled(fixedNow())
	if err != nil {
		t.Fatalf("MarkCancelled: %v", err)
	}
	if err := s1.Update(ctx, cancelled); err != nil {
		t.Fatalf("Update cancelled: %v", err)
	}
	if err := s1.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	// Boot 2: ListPending must return only the surviving pending row.
	s2 := openStore(t, dbPath)
	pending, err := s2.ListPending(ctx, "default")
	if err != nil {
		t.Fatalf("ListPending: %v", err)
	}
	if len(pending) != 1 || pending[0].ID() != "alive" {
		t.Fatalf("pending=%+v want [alive]", pending)
	}
	if pending[0].State() != domain.SchedulePending {
		t.Fatalf("state=%v want pending", pending[0].State())
	}
	if !pending[0].FireAt().Equal(fixedNow().Add(time.Hour)) {
		t.Fatalf("fireAt drifted: %v", pending[0].FireAt())
	}

	// Full list across all states preserves terminal row.
	all, err := s2.List(ctx, "default", 0, 100)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(all) != 2 {
		t.Fatalf("List len=%d want 2", len(all))
	}
	var sawCancelled bool
	for _, r := range all {
		if r.ID() == "gone" && r.State() == domain.ScheduleCancelled {
			sawCancelled = true
		}
	}
	if !sawCancelled {
		t.Fatalf("terminal row not preserved: %+v", all)
	}
}

func TestScheduleStoreGetMissingReturnsErrNotFound(t *testing.T) {
	t.Parallel()
	dbPath := filepath.Join(t.TempDir(), "scheduled.db")
	s := openStore(t, dbPath)
	if _, err := s.Get(context.Background(), "default", "nope"); !errors.Is(err, sqliteschedule.ErrNotFound) {
		t.Fatalf("want ErrNotFound, got %v", err)
	}
}

// TestRestartAfterClaimNeverResends is the end-to-end at-most-once check on
// the real store: a row claimed for sending when the daemon died is, after a
// reopen and ScheduleRunner.Start, send_uncertain on disk, not armed, and
// its Fire callback never runs. The state values persist as stored integers
// (no schema change): sending=5, send_uncertain=6.
func TestRestartAfterClaimNeverResends(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "scheduled.db")
	s1, err := sqliteschedule.Open(ctx, dbPath)
	if err != nil {
		t.Fatal(err)
	}
	ss := mustPending(t, "sch-crash", time.Hour)
	if err := s1.Put(ctx, ss); err != nil {
		t.Fatal(err)
	}
	claimed, _ := ss.MarkSending(fixedNow().Add(time.Hour))
	if won, err := s1.Claim(ctx, claimed); err != nil || !won {
		t.Fatalf("Claim = %v, %v", won, err)
	}
	if won, _ := s1.Claim(ctx, claimed); won {
		t.Fatal("a pending-only claim succeeded twice")
	}
	if _, err := s1.Claim(ctx, ss); err == nil {
		t.Fatal("Claim accepted a row that is not in state sending")
	}
	_ = s1.Close() // the "crash": no fired/failed ever recorded

	s2 := openStore(t, dbPath)
	fired := make(chan string, 1)
	r := app.NewScheduleRunner(s2, "default", func(_ context.Context, _, id string) { fired <- id })
	r.Now = func() time.Time { return fixedNow().Add(2 * time.Hour) }
	if err := r.Start(ctx); err != nil {
		t.Fatal(err)
	}
	defer r.Stop()

	got, err := s2.Get(ctx, "default", "sch-crash")
	if err != nil || got.State() != domain.ScheduleSendUncertain || got.LastError() == "" {
		t.Fatalf("after restart: %v %q, %v; want send_uncertain with a reason", got.State(), got.LastError(), err)
	}
	if int(domain.ScheduleSending) != 5 || int(domain.ScheduleSendUncertain) != 6 {
		t.Errorf("stored state integers changed: %d %d", domain.ScheduleSending, domain.ScheduleSendUncertain)
	}
	select {
	case id := <-fired:
		t.Fatalf("row %q was fired after the restart", id)
	case <-time.After(100 * time.Millisecond):
	}
	if rows, _ := s2.List(ctx, "default", domain.ScheduleSendUncertain, 10); len(rows) != 1 {
		t.Errorf("uncertain list has %d rows, want 1", len(rows))
	}
}

// TestUpdateFromRefusesAStaleState: on the real store a cancel built from
// a stale pending read cannot overwrite a row the timer has since claimed.
func TestUpdateFromRefusesAStaleState(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := openStore(t, filepath.Join(t.TempDir(), "scheduled.db"))
	ss := mustPending(t, "sch-cas", time.Hour)
	_ = s.Put(ctx, ss)
	stale, _ := ss.MarkCancelled(fixedNow())
	claimed, _ := ss.MarkSending(fixedNow())
	if won, _ := s.Claim(ctx, claimed); !won {
		t.Fatal("claim lost")
	}
	if won, err := s.UpdateFrom(ctx, domain.SchedulePending, stale); err != nil || won {
		t.Fatalf("UpdateFrom(pending) over a claim = %v, %v; want false, nil", won, err)
	}
	if got, _ := s.Get(ctx, "default", "sch-cas"); got.State() != domain.ScheduleSending {
		t.Fatalf("state = %s, want sending", got.State())
	}
	// And the reverse: a cancelled row can no longer be claimed, so it is never sent.
	ss2 := mustPending(t, "sch-cas2", time.Hour)
	_ = s.Put(ctx, ss2)
	c2, _ := ss2.MarkCancelled(fixedNow())
	if won, err := s.UpdateFrom(ctx, domain.SchedulePending, c2); err != nil || !won {
		t.Fatalf("cancel of a pending row = %v, %v", won, err)
	}
	cl2, _ := ss2.MarkSending(fixedNow())
	if won, _ := s.Claim(ctx, cl2); won {
		t.Fatal("a cancelled row was claimed (it would be sent)")
	}
}

// TestClaimRefusesARescheduledRow (p8 L1): a claim built from a load taken
// before an update moved fire_at loses, so the row is not sent at the old time.
func TestClaimRefusesARescheduledRow(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := openStore(t, filepath.Join(t.TempDir(), "scheduled.db"))
	loaded := mustPending(t, "sch-r", time.Hour)
	_ = s.Put(ctx, loaded)
	later, _ := loaded.Reschedule(fixedNow().Add(48*time.Hour), fixedNow())
	if won, err := s.UpdateFrom(ctx, domain.SchedulePending, later); err != nil || !won {
		t.Fatalf("reschedule = %v, %v", won, err)
	}
	stale, _ := loaded.MarkSending(fixedNow())
	if won, err := s.Claim(ctx, stale); err != nil || won {
		t.Fatalf("claim from a stale load = %v, %v; want false, nil", won, err)
	}
	if got, _ := s.Get(ctx, "default", "sch-r"); got.State() != domain.SchedulePending || !got.FireAt().Equal(later.FireAt()) {
		t.Fatalf("row = %s at %s, want pending at the new time", got.State(), got.FireAt())
	}
}

// TestListPendingIsSoonestFirstAtTheLimit (p8 L2): pending rows come soonest
// first so a cut at the limit drops the farthest; other states stay newest first.
func TestListPendingIsSoonestFirstAtTheLimit(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := openStore(t, filepath.Join(t.TempDir(), "scheduled.db"))
	for i := 5; i >= 1; i-- {
		_ = s.Put(ctx, mustPending(t, fmt.Sprintf("p%d", i), time.Duration(i)*time.Hour))
	}
	for i := 1; i <= 3; i++ {
		f, _ := mustPending(t, fmt.Sprintf("f%d", i), time.Duration(i)*time.Hour).MarkSending(fixedNow())
		f, _ = f.MarkFired(fixedNow())
		_ = s.Put(ctx, f)
	}
	ids := func(st domain.ScheduledState) []string {
		rows, _ := s.List(ctx, "default", st, 2)
		out := make([]string, 0, len(rows))
		for _, r := range rows {
			out = append(out, r.ID())
		}
		return out
	}
	if got := ids(domain.SchedulePending); !slices.Equal(got, []string{"p1", "p2"}) {
		t.Errorf("pending limit 2 = %v, want p1,p2", got)
	}
	if got := ids(domain.ScheduleFired); !slices.Equal(got, []string{"f3", "f2"}) {
		t.Errorf("fired limit 2 = %v, want newest first f3,f2", got)
	}
}
