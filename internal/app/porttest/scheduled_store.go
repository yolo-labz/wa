package porttest

import (
	"context"
	"testing"
	"time"

	"github.com/yolo-labz/wa/v2/internal/app"
	"github.com/yolo-labz/wa/v2/internal/domain"
)

// ScheduledStoreFactory returns a fresh ScheduledStore for one sub-test.
type ScheduledStoreFactory func(t *testing.T) app.ScheduledStore

// RunScheduledStoreContract exercises ScheduledStore (FR-111).
//
//	SS1 Put + Get round-trip preserves state.
//	SS2 Put of duplicate id returns a non-nil error.
//	SS3 ListPending returns a pending row, then an empty slice after Update→fired.
//	SS5 Claim moves pending to sending once (compare-and-set); a second claim loses.
//	SS6 UpdateFrom replaces a row only while it is in the wanted state; otherwise it loses.
//	SS4 Delete removes the row (subsequent Get returns a non-nil error).
func RunScheduledStoreContract(t *testing.T, factory ScheduledStoreFactory) {
	t.Helper()
	ctx := context.Background()
	jid := domain.MustJID("5511999999999")
	now := time.Unix(1_700_000_000, 0).UTC()
	fireAt := now.Add(time.Hour)

	mk := func(id string) domain.ScheduledSend {
		s, err := domain.NewScheduledSend(domain.ScheduledSendSpec{
			ID: id, Profile: "default", Kind: domain.ScheduleKindSendText, To: jid,
			Body: "hi", FireAt: fireAt, Now: now,
		})
		if err != nil {
			t.Fatalf("fixture: NewScheduledSend(%q): %v", id, err)
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

	t.Run("SS5_claim_is_compare_and_set", func(t *testing.T) {
		s := factory(t)
		sch := mk("sch-ss5")
		if err := s.Put(ctx, sch); err != nil {
			t.Fatalf("Put: %v", err)
		}
		claimed, err := sch.MarkSending(fireAt)
		if err != nil {
			t.Fatalf("MarkSending: %v", err)
		}
		if won, err := s.Claim(ctx, claimed); err != nil || !won {
			reportf(t, "ScheduledStore", "Claim", "SS5", "first claim of a pending row wins", "won=false or error")
		}
		if got, _ := s.Get(ctx, "default", "sch-ss5"); got.State() != domain.ScheduleSending {
			reportf(t, "ScheduledStore", "Claim", "SS5", "stored state sending", got.State().String())
		}
		if won, err := s.Claim(ctx, claimed); err != nil || won {
			reportf(t, "ScheduledStore", "Claim", "SS5", "second claim loses without error", "won or error")
		}
		pending, _ := s.ListPending(ctx, "default")
		for _, p := range pending {
			if p.ID() == "sch-ss5" {
				reportf(t, "ScheduledStore", "ListPending", "SS5", "claimed row absent", "claimed row present")
			}
		}
	})

	t.Run("SS6_update_from_is_compare_and_set", func(t *testing.T) {
		s := factory(t)
		sch := mk("sch-ss6")
		if err := s.Put(ctx, sch); err != nil {
			t.Fatalf("Put: %v", err)
		}
		cancelled, _ := sch.MarkCancelled(fireAt)
		claimed, _ := sch.MarkSending(fireAt)
		if won, err := s.Claim(ctx, claimed); err != nil || !won {
			t.Fatalf("Claim: %v %v", won, err)
		}
		if won, err := s.UpdateFrom(ctx, domain.SchedulePending, cancelled); err != nil || won {
			reportf(t, "ScheduledStore", "UpdateFrom", "SS6", "loses against a claimed row", "won or error")
		}
		if got, _ := s.Get(ctx, "default", "sch-ss6"); got.State() != domain.ScheduleSending {
			reportf(t, "ScheduledStore", "UpdateFrom", "SS6", "row still sending", got.State().String())
		}
		if won, err := s.UpdateFrom(ctx, domain.ScheduleSending, cancelled); err != nil || !won {
			reportf(t, "ScheduledStore", "UpdateFrom", "SS6", "wins from the actual state", "lost or error")
		}
		other := mk("sch-ss6b")
		_ = s.Put(ctx, other)
		oc, _ := other.MarkCancelled(fireAt)
		if won, err := s.UpdateFrom(ctx, domain.SchedulePending, oc); err != nil || !won {
			reportf(t, "ScheduledStore", "UpdateFrom", "SS6", "wins on a pending row", "lost or error")
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
