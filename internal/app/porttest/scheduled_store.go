package porttest

import (
	"context"
	"testing"
	"time"

	"github.com/yolo-labz/wa/internal/app"
	"github.com/yolo-labz/wa/internal/domain"
)

// ScheduledStoreFactory returns a fresh ScheduledStore for one sub-test.
type ScheduledStoreFactory func(t *testing.T) app.ScheduledStore

// RunScheduledStoreContract exercises ScheduledStore (FR-111).
//
//	SS1 Put + Get round-trip preserves state.
//	SS2 Put of duplicate id returns a non-nil error.
//	SS3 ListPending returns a pending row, then an empty slice after Update→fired.
//	SS4 Delete removes the row (subsequent Get returns a non-nil error).
func RunScheduledStoreContract(t *testing.T, factory ScheduledStoreFactory) {
	t.Helper()
	ctx := context.Background()
	jid := domain.MustJID("5511999999999")
	now := time.Unix(1_700_000_000, 0).UTC()
	fireAt := now.Add(time.Hour)

	mk := func(id string) domain.ScheduledSend {
		s, err := domain.NewScheduledSend(id, "default", domain.ScheduleKindSendText, jid, "hi", "", fireAt, now)
		if err != nil {
			panic(err)
		}
		return s
	}

	t.Run("SS1_put_get_round_trip", func(t *testing.T) {
		s := factory(t)
		sch := mk("sch-ss1")
		if err := s.Put(ctx, sch); err != nil {
			t.Fatalf("Put: %v", err)
		}
		got, err := s.Get(ctx, "default", "sch-ss1")
		if err != nil {
			reportf(t, "ScheduledStore", "Get", "SS1", "nil error", err.Error())
			return
		}
		if got.ID() != sch.ID() || got.State() != sch.State() {
			reportf(t, "ScheduledStore", "Get", "SS1", "round-trip preserved", "mutated")
		}
	})

	t.Run("SS2_duplicate_put_rejected", func(t *testing.T) {
		s := factory(t)
		sch := mk("sch-ss2")
		if err := s.Put(ctx, sch); err != nil {
			t.Fatalf("first Put: %v", err)
		}
		if err := s.Put(ctx, sch); err == nil {
			reportf(t, "ScheduledStore", "Put", "SS2", "non-nil error on duplicate", "nil")
		}
	})

	t.Run("SS3_list_pending_reflects_state", func(t *testing.T) {
		s := factory(t)
		sch := mk("sch-ss3")
		if err := s.Put(ctx, sch); err != nil {
			t.Fatalf("Put: %v", err)
		}
		pending, err := s.ListPending(ctx, "default")
		if err != nil {
			reportf(t, "ScheduledStore", "ListPending", "SS3", "nil error", err.Error())
			return
		}
		if len(pending) < 1 {
			reportf(t, "ScheduledStore", "ListPending", "SS3", "≥1 row after Put", "0 rows")
		}
		fired, ferr := sch.MarkFired(fireAt.Add(time.Second))
		if ferr != nil {
			t.Fatalf("MarkFired: %v", ferr)
		}
		if err := s.Update(ctx, fired); err != nil {
			t.Fatalf("Update: %v", err)
		}
		pending2, err := s.ListPending(ctx, "default")
		if err != nil {
			reportf(t, "ScheduledStore", "ListPending", "SS3", "nil error after fire", err.Error())
			return
		}
		for _, p := range pending2 {
			if p.ID() == sch.ID() {
				reportf(t, "ScheduledStore", "ListPending", "SS3", "fired row absent", "fired row present")
			}
		}
	})

	t.Run("SS4_delete_then_get_not_found", func(t *testing.T) {
		s := factory(t)
		sch := mk("sch-ss4")
		if err := s.Put(ctx, sch); err != nil {
			t.Fatalf("Put: %v", err)
		}
		if err := s.Delete(ctx, "default", "sch-ss4"); err != nil {
			reportf(t, "ScheduledStore", "Delete", "SS4", "nil error", err.Error())
		}
		if _, err := s.Get(ctx, "default", "sch-ss4"); err == nil {
			reportf(t, "ScheduledStore", "Get", "SS4", "non-nil error after delete", "nil")
		}
	})
}
