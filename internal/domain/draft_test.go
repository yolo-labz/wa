package domain

import (
	"errors"
	"testing"
)

func testTarget(t *testing.T) JID {
	t.Helper()
	j, err := Parse("5511999999999@s.whatsapp.net")
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	return j
}

func TestDraftStateTransitions(t *testing.T) {
	now := int64(1_700_000_000)
	target := testTarget(t)

	t.Run("new draft invariants", func(t *testing.T) {
		d, err := NewDraft("d_01", "default", DraftKindSend, `{"body":"hi"}`, target, now)
		if err != nil {
			t.Fatalf("NewDraft: %v", err)
		}
		if d.State != DraftPendingReview {
			t.Fatalf("state = %v, want pending_review", d.State)
		}
		if d.ExpiresAt != now+DraftExpiryWindow {
			t.Fatalf("expires_at = %d, want %d", d.ExpiresAt, now+DraftExpiryWindow)
		}
	})

	t.Run("approve once", func(t *testing.T) {
		d, _ := NewDraft("d_02", "default", DraftKindSend, `{"body":"hi"}`, target, now)
		d2, err := d.Approve(now+1, DraftDeciderCLI)
		if err != nil {
			t.Fatalf("first Approve: %v", err)
		}
		if d2.State != DraftApproved {
			t.Fatalf("state = %v, want approved", d2.State)
		}
		if d2.DecidedBy != DraftDeciderCLI {
			t.Fatalf("decided_by = %q, want cli", d2.DecidedBy)
		}
		if _, err := d2.Approve(now+2, DraftDeciderCLI); !errors.Is(err, ErrDraftState) {
			t.Fatalf("second Approve err = %v, want ErrDraftState", err)
		}
	})

	t.Run("reject once with reason", func(t *testing.T) {
		d, _ := NewDraft("d_03", "default", DraftKindSend, `{"body":"hi"}`, target, now)
		d2, err := d.Reject(now+1, DraftDeciderSkill, "user declined")
		if err != nil {
			t.Fatalf("Reject: %v", err)
		}
		if d2.State != DraftRejected || d2.Reason != "user declined" {
			t.Fatalf("state=%v reason=%q, want rejected 'user declined'", d2.State, d2.Reason)
		}
		if _, err := d2.Reject(now+2, DraftDeciderSkill, ""); !errors.Is(err, ErrDraftState) {
			t.Fatalf("second Reject err = %v, want ErrDraftState", err)
		}
		if _, err := d2.Approve(now+2, DraftDeciderCLI); !errors.Is(err, ErrDraftState) {
			t.Fatalf("Approve-after-reject err = %v, want ErrDraftState", err)
		}
	})

	t.Run("approve rejects timeout decider", func(t *testing.T) {
		d, _ := NewDraft("d_04", "default", DraftKindSend, `{"body":"hi"}`, target, now)
		if _, err := d.Approve(now+1, DraftDeciderTimeout); !errors.Is(err, ErrDraftState) {
			t.Fatalf("Approve with timeout decider err = %v, want ErrDraftState", err)
		}
	})

	t.Run("expire at expiry window", func(t *testing.T) {
		d, _ := NewDraft("d_05", "default", DraftKindSend, `{"body":"hi"}`, target, now)
		if _, err := d.ExpireAt(d.ExpiresAt - 1); !errors.Is(err, ErrDraftState) {
			t.Fatalf("early ExpireAt err = %v, want ErrDraftState", err)
		}
		d2, err := d.ExpireAt(d.ExpiresAt)
		if err != nil {
			t.Fatalf("ExpireAt at boundary: %v", err)
		}
		if d2.State != DraftExpired || d2.DecidedBy != DraftDeciderTimeout {
			t.Fatalf("state=%v decided_by=%q, want expired timeout", d2.State, d2.DecidedBy)
		}
	})

	t.Run("expire is idempotent on terminal", func(t *testing.T) {
		d, _ := NewDraft("d_06", "default", DraftKindSend, `{"body":"hi"}`, target, now)
		d2, _ := d.Approve(now+1, DraftDeciderCLI)
		d3, err := d2.ExpireAt(d.ExpiresAt + 60)
		if err != nil {
			t.Fatalf("ExpireAt on approved: %v", err)
		}
		if d3.State != DraftApproved {
			t.Fatalf("terminal state mutated to %v", d3.State)
		}
	})

	t.Run("group_create requires zero target", func(t *testing.T) {
		if _, err := NewDraft("d_07", "default", DraftKindGroupCreate, `{"name":"x"}`, target, now); !errors.Is(err, ErrDraftInvariant) {
			t.Fatalf("group_create with target jid err = %v, want ErrDraftInvariant", err)
		}
		if _, err := NewDraft("d_08", "default", DraftKindGroupCreate, `{"name":"x"}`, JID{}, now); err != nil {
			t.Fatalf("group_create with zero target: %v", err)
		}
	})

	t.Run("always-draft kinds", func(t *testing.T) {
		cases := []struct {
			kind  DraftKind
			force bool
		}{
			{DraftKindGroupCreate, true},
			{DraftKindGroupAddParticipant, true},
			{DraftKindSend, false},
			{DraftKindSendMedia, false},
		}
		for _, tc := range cases {
			if got := tc.kind.AlwaysDraft(); got != tc.force {
				t.Errorf("AlwaysDraft(%s) = %v, want %v", tc.kind, got, tc.force)
			}
		}
	})
}
