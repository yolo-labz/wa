package porttest

import (
	"context"
	"testing"
	"time"

	"github.com/yolo-labz/wa/internal/app"
	"github.com/yolo-labz/wa/internal/domain"
)

// LabelManagerFactory returns a fresh LabelManager for one sub-test.
type LabelManagerFactory func(t *testing.T) app.LabelManager

// RunLabelManagerContract runs every contract clause for LabelManager per
// FR-120..122. The suite is adapter-agnostic: both an in-memory fake and
// the whatsmeow-backed adapter MUST pass it.
//
//	LM1 CreateLabel returns a Label with a non-empty id, echoing the inputs.
//	LM2 ListLabels includes every created label exactly once.
//	LM3 Assign + ListAssignments round-trips a chat assignment.
//	LM4 Assign + ListAssignments round-trips a message assignment.
//	LM5 Unassign removes one specific assignment without touching others.
//	LM6 DeleteLabel + ListLabels removes the label from the list.
//	LM7 Assign is idempotent (re-assigning the same pair is a no-op).
//	LM8 Unassign on a missing assignment is a no-op (no error).
func RunLabelManagerContract(t *testing.T, factory LabelManagerFactory) {
	t.Helper()
	ctx := context.Background()
	chat, err := domain.Parse("5511999999999@s.whatsapp.net")
	if err != nil {
		t.Fatalf("parse chat: %v", err)
	}
	now := time.Unix(1_700_000_000, 0).UTC()

	t.Run("LM1 CreateLabel echoes inputs", func(t *testing.T) {
		m := factory(t)
		l, err := m.CreateLabel(ctx, "default", "Work", 3)
		if err != nil {
			t.Fatalf("CreateLabel: %v", err)
		}
		if l.ID() == "" {
			reportf(t, "LabelManager", "CreateLabel", "LM1", "non-empty id", "empty id")
		}
		if l.Name() != "Work" {
			reportf(t, "LabelManager", "CreateLabel", "LM1", "Work", l.Name())
		}
		if l.ColorIndex() != 3 {
			reportf(t, "LabelManager", "CreateLabel", "LM1", "color=3", itoa(l.ColorIndex()))
		}
	})

	t.Run("LM2 ListLabels includes created", func(t *testing.T) {
		m := factory(t)
		_, _ = m.CreateLabel(ctx, "default", "Work", 1)
		_, _ = m.CreateLabel(ctx, "default", "Family", 2)
		got, err := m.ListLabels(ctx, "default")
		if err != nil {
			t.Fatalf("ListLabels: %v", err)
		}
		if len(got) != 2 {
			reportf(t, "LabelManager", "ListLabels", "LM2", "2 labels", itoa(len(got)))
		}
	})

	t.Run("LM3 Assign chat round-trips", func(t *testing.T) {
		m := factory(t)
		lbl, _ := m.CreateLabel(ctx, "default", "Work", 1)
		a, err := domain.NewChatLabelAssignment(lbl.ID(), chat, now)
		if err != nil {
			t.Fatalf("NewChatLabelAssignment: %v", err)
		}
		if err := m.Assign(ctx, "default", a); err != nil {
			t.Fatalf("Assign: %v", err)
		}
		got, err := m.ListAssignments(ctx, "default", lbl.ID())
		if err != nil {
			t.Fatalf("ListAssignments: %v", err)
		}
		if len(got) != 1 || got[0].TargetKind() != domain.LabelTargetChat {
			reportf(t, "LabelManager", "ListAssignments", "LM3",
				"1 chat assignment", itoa(len(got)))
		}
	})

	t.Run("LM4 Assign message round-trips", func(t *testing.T) {
		m := factory(t)
		lbl, _ := m.CreateLabel(ctx, "default", "Work", 1)
		a, err := domain.NewMessageLabelAssignment(lbl.ID(), chat, domain.MessageID("msg_01"), now)
		if err != nil {
			t.Fatalf("NewMessageLabelAssignment: %v", err)
		}
		if err := m.Assign(ctx, "default", a); err != nil {
			t.Fatalf("Assign: %v", err)
		}
		got, err := m.ListAssignments(ctx, "default", lbl.ID())
		if err != nil {
			t.Fatalf("ListAssignments: %v", err)
		}
		if len(got) != 1 || got[0].MessageID() != "msg_01" {
			reportf(t, "LabelManager", "ListAssignments", "LM4",
				"1 message assignment", itoa(len(got)))
		}
	})

	t.Run("LM5 Unassign removes one assignment", func(t *testing.T) {
		m := factory(t)
		lbl, _ := m.CreateLabel(ctx, "default", "Work", 1)
		chatA, _ := domain.NewChatLabelAssignment(lbl.ID(), chat, now)
		msgA, _ := domain.NewMessageLabelAssignment(lbl.ID(), chat, domain.MessageID("msg_01"), now)
		_ = m.Assign(ctx, "default", chatA)
		_ = m.Assign(ctx, "default", msgA)

		if err := m.Unassign(ctx, "default", chatA); err != nil {
			t.Fatalf("Unassign: %v", err)
		}
		got, err := m.ListAssignments(ctx, "default", lbl.ID())
		if err != nil {
			t.Fatalf("ListAssignments: %v", err)
		}
		if len(got) != 1 || got[0].TargetKind() != domain.LabelTargetMessage {
			reportf(t, "LabelManager", "Unassign", "LM5",
				"1 remaining message assignment", itoa(len(got)))
		}
	})

	t.Run("LM6 DeleteLabel removes from list", func(t *testing.T) {
		m := factory(t)
		l, _ := m.CreateLabel(ctx, "default", "Throwaway", 0)
		if err := m.DeleteLabel(ctx, "default", l.ID()); err != nil {
			t.Fatalf("DeleteLabel: %v", err)
		}
		got, err := m.ListLabels(ctx, "default")
		if err != nil {
			t.Fatalf("ListLabels: %v", err)
		}
		for _, x := range got {
			if x.ID() == l.ID() {
				reportf(t, "LabelManager", "ListLabels", "LM6",
					"deleted label absent", "still present")
			}
		}
	})

	t.Run("LM7 Assign is idempotent", func(t *testing.T) {
		m := factory(t)
		lbl, _ := m.CreateLabel(ctx, "default", "Work", 1)
		a, _ := domain.NewChatLabelAssignment(lbl.ID(), chat, now)
		if err := m.Assign(ctx, "default", a); err != nil {
			t.Fatalf("Assign 1: %v", err)
		}
		if err := m.Assign(ctx, "default", a); err != nil {
			t.Fatalf("Assign 2 (idempotent): %v", err)
		}
		got, _ := m.ListAssignments(ctx, "default", lbl.ID())
		if len(got) != 1 {
			reportf(t, "LabelManager", "Assign", "LM7",
				"exactly 1 assignment after double-Assign", itoa(len(got)))
		}
	})

	t.Run("LM8 Unassign missing is noop", func(t *testing.T) {
		m := factory(t)
		lbl, _ := m.CreateLabel(ctx, "default", "Work", 1)
		a, _ := domain.NewChatLabelAssignment(lbl.ID(), chat, now)
		if err := m.Unassign(ctx, "default", a); err != nil {
			reportf(t, "LabelManager", "Unassign", "LM8", "nil (idempotent)", errString(err))
		}
	})
}
