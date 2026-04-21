package sqliteschedule_test

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/yolo-labz/wa/internal/adapters/secondary/sqliteschedule"
	"github.com/yolo-labz/wa/internal/domain"
)

func fixedNow() time.Time { return time.Date(2026, 4, 20, 12, 0, 0, 0, time.UTC) }

func mustPending(t *testing.T, id string, fireIn time.Duration) domain.ScheduledSend {
	t.Helper()
	jid, err := domain.Parse("5511999999999")
	if err != nil {
		t.Fatalf("parse jid: %v", err)
	}
	ss, err := domain.NewScheduledSend(id, "default", domain.ScheduleKindSendText, jid,
		"hello", "", fixedNow().Add(fireIn), fixedNow())
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
