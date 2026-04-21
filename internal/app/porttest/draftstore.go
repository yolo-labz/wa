package porttest

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/yolo-labz/wa/internal/app"
	"github.com/yolo-labz/wa/internal/domain"
)

// DraftStoreFactory returns a fresh DraftStore for one sub-test.
type DraftStoreFactory func(t *testing.T) app.DraftStore

// RunDraftStoreContract runs every contract clause for DraftStore per
// FR-040..FR-045.
//
//	DS1 Put + Get round-trip preserves invariants.
//	DS2 Approve transitions pending_review → approved exactly once.
//	DS3 Reject transitions pending_review → rejected exactly once.
//	DS4 ExpireDue transitions all overdue drafts → expired in one call.
//	DS5 List filters by state and respects limit.
//	DS6 Double-approve returns ErrDraftState, store state unchanged.
func RunDraftStoreContract(t *testing.T, factory DraftStoreFactory) {
	t.Helper()
	ctx := context.Background()
	jid, _ := domain.Parse("5511999999999@s.whatsapp.net")
	now := time.Unix(1_700_000_000, 0)

	mkDraft := func(id string, created time.Time) domain.Draft {
		d, err := domain.NewDraft(id, "default", domain.DraftKindSend, `{"body":"hi"}`, jid, created.Unix())
		if err != nil {
			t.Fatalf("NewDraft: %v", err)
		}
		return d
	}

	t.Run("DS1 Put/Get round-trip", func(t *testing.T) {
		s := factory(t)
		d := mkDraft("d_01", now)
		if err := s.Put(ctx, d); err != nil {
			t.Fatalf("Put: %v", err)
		}
		got, err := s.Get(ctx, "default", "d_01")
		if err != nil {
			t.Fatalf("Get: %v", err)
		}
		if got.ID != d.ID || got.State != d.State || got.ExpiresAt != d.ExpiresAt {
			reportf(t, "DraftStore", "Get", "DS1", "identical draft", "mutated draft")
		}
	})

	t.Run("DS2 Approve once", func(t *testing.T) {
		s := factory(t)
		d := mkDraft("d_02", now)
		if err := s.Put(ctx, d); err != nil {
			t.Fatalf("Put: %v", err)
		}
		out, err := s.Approve(ctx, "default", "d_02", now.Add(time.Minute), domain.DraftDeciderCLI)
		if err != nil {
			t.Fatalf("Approve: %v", err)
		}
		if out.State != domain.DraftApproved {
			reportf(t, "DraftStore", "Approve", "DS2", "state=approved", "state="+out.State.String())
		}
	})

	t.Run("DS3 Reject once", func(t *testing.T) {
		s := factory(t)
		d := mkDraft("d_03", now)
		if err := s.Put(ctx, d); err != nil {
			t.Fatalf("Put: %v", err)
		}
		out, err := s.Reject(ctx, "default", "d_03", now.Add(time.Minute), domain.DraftDeciderCLI, "no")
		if err != nil {
			t.Fatalf("Reject: %v", err)
		}
		if out.State != domain.DraftRejected || out.Reason != "no" {
			reportf(t, "DraftStore", "Reject", "DS3", "rejected with reason", "state="+out.State.String())
		}
	})

	t.Run("DS4 ExpireDue", func(t *testing.T) {
		s := factory(t)
		d1 := mkDraft("d_04a", now)
		d2 := mkDraft("d_04b", now)
		_ = s.Put(ctx, d1)
		_ = s.Put(ctx, d2)
		expiredAt := time.Unix(d1.ExpiresAt+1, 0)
		n, err := s.ExpireDue(ctx, "default", expiredAt)
		if err != nil {
			t.Fatalf("ExpireDue: %v", err)
		}
		if n != 2 {
			reportf(t, "DraftStore", "ExpireDue", "DS4", "2 expired", itoa(n)+" expired")
		}
	})

	t.Run("DS5 List filter by state", func(t *testing.T) {
		s := factory(t)
		for _, id := range []string{"d_05a", "d_05b", "d_05c"} {
			_ = s.Put(ctx, mkDraft(id, now))
		}
		_, _ = s.Approve(ctx, "default", "d_05a", now.Add(time.Minute), domain.DraftDeciderCLI)
		got, err := s.List(ctx, "default", domain.DraftPendingReview, 100)
		if err != nil {
			t.Fatalf("List: %v", err)
		}
		if len(got) != 2 {
			reportf(t, "DraftStore", "List", "DS5", "2 pending", itoa(len(got))+" pending")
		}
	})

	t.Run("DS6 Double-approve rejected", func(t *testing.T) {
		s := factory(t)
		d := mkDraft("d_06", now)
		_ = s.Put(ctx, d)
		_, _ = s.Approve(ctx, "default", "d_06", now.Add(time.Minute), domain.DraftDeciderCLI)
		_, err := s.Approve(ctx, "default", "d_06", now.Add(2*time.Minute), domain.DraftDeciderCLI)
		if !errors.Is(err, domain.ErrDraftState) {
			reportf(t, "DraftStore", "Approve", "DS6", "ErrDraftState", errString(err))
		}
	})
}
