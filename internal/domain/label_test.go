package domain

import (
	"errors"
	"strings"
	"testing"
	"time"
)

func TestNewLabel_Valid(t *testing.T) {
	now := time.Date(2026, 4, 20, 12, 0, 0, 0, time.UTC)
	l, err := NewLabel("lbl_01", "  Work  ", 3, now)
	if err != nil {
		t.Fatalf("NewLabel: %v", err)
	}
	if l.Name() != "Work" {
		t.Errorf("name not trimmed: %q", l.Name())
	}
	if l.ColorIndex() != 3 {
		t.Errorf("colorIndex: got %d want 3", l.ColorIndex())
	}
	if !l.UpdatedAt().Equal(l.CreatedAt()) {
		t.Errorf("fresh label: updatedAt must equal createdAt")
	}
}

func TestNewLabel_InvalidName(t *testing.T) {
	now := time.Now()
	for _, tc := range []struct {
		name string
		want error
	}{
		{"", ErrLabelNameEmpty},
		{"   ", ErrLabelNameEmpty},
		{strings.Repeat("x", LabelNameMaxLen+1), ErrLabelNameTooLong},
	} {
		if _, err := NewLabel("lbl_bad", tc.name, 0, now); !errors.Is(err, tc.want) {
			t.Errorf("NewLabel(%q) err=%v want %v", tc.name, err, tc.want)
		}
	}
}

func TestNewLabel_InvalidColor(t *testing.T) {
	now := time.Now()
	for _, idx := range []int{-1, LabelPaletteSize, 999} {
		if _, err := NewLabel("lbl_bad", "Work", idx, now); !errors.Is(err, ErrLabelColorOutOfRange) {
			t.Errorf("NewLabel color=%d: got %v want ErrLabelColorOutOfRange", idx, err)
		}
	}
}

func TestNewLabel_MissingID(t *testing.T) {
	if _, err := NewLabel("", "Work", 0, time.Now()); err == nil {
		t.Error("want error on empty id")
	}
}

func TestLabelRenameAndRecolor(t *testing.T) {
	now := time.Date(2026, 4, 20, 12, 0, 0, 0, time.UTC)
	l, err := NewLabel("lbl_01", "Work", 3, now)
	if err != nil {
		t.Fatalf("NewLabel: %v", err)
	}
	later := now.Add(time.Hour)
	l2, err := l.Rename(" Family ", later)
	if err != nil {
		t.Fatalf("Rename: %v", err)
	}
	if l2.Name() != "Family" {
		t.Errorf("rename: got %q", l2.Name())
	}
	if !l2.UpdatedAt().Equal(later.UTC()) {
		t.Errorf("rename: updatedAt not refreshed")
	}
	// Original value unchanged (value semantics).
	if l.Name() != "Work" {
		t.Errorf("rename mutated original: %q", l.Name())
	}

	l3, err := l2.Recolor(7, later.Add(time.Minute))
	if err != nil {
		t.Fatalf("Recolor: %v", err)
	}
	if l3.ColorIndex() != 7 {
		t.Errorf("recolor: %d", l3.ColorIndex())
	}

	if _, err := l3.Recolor(-1, later); !errors.Is(err, ErrLabelColorOutOfRange) {
		t.Errorf("recolor -1: got %v", err)
	}
	if _, err := l3.Rename("", later); !errors.Is(err, ErrLabelNameEmpty) {
		t.Errorf("rename empty: got %v", err)
	}
}

func TestHydrateLabel(t *testing.T) {
	created := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	updated := time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC)
	l := HydrateLabel("lbl_h", "already-valid", 5, created, updated)
	if l.Name() != "already-valid" || l.ColorIndex() != 5 {
		t.Errorf("hydrate mismatch: %+v", l)
	}
	if !l.CreatedAt().Equal(created) || !l.UpdatedAt().Equal(updated) {
		t.Errorf("hydrate timestamps")
	}
}

func TestNewChatLabelAssignment(t *testing.T) {
	jid, err := Parse("5511999999999@s.whatsapp.net")
	if err != nil {
		t.Fatalf("parse jid: %v", err)
	}
	now := time.Unix(1_700_000_000, 0).UTC()
	a, err := NewChatLabelAssignment("lbl_01", jid, now)
	if err != nil {
		t.Fatalf("NewChatLabelAssignment: %v", err)
	}
	if a.TargetKind() != LabelTargetChat {
		t.Errorf("kind=%s want chat", a.TargetKind())
	}
	if a.MessageID() != "" {
		t.Errorf("chat assignment must leave messageID empty, got %q", a.MessageID())
	}
	if a.Chat() != jid {
		t.Errorf("chat mismatch")
	}
}

func TestNewChatLabelAssignment_Invalid(t *testing.T) {
	jid, _ := Parse("5511999999999@s.whatsapp.net")
	now := time.Now()
	if _, err := NewChatLabelAssignment("", jid, now); err == nil {
		t.Error("want error on empty labelID")
	}
	if _, err := NewChatLabelAssignment("lbl", JID{}, now); err == nil {
		t.Error("want error on zero chat")
	}
}

func TestNewMessageLabelAssignment(t *testing.T) {
	jid, _ := Parse("5511999999999@s.whatsapp.net")
	now := time.Now()
	a, err := NewMessageLabelAssignment("lbl_01", jid, MessageID("msg_01"), now)
	if err != nil {
		t.Fatalf("NewMessageLabelAssignment: %v", err)
	}
	if a.TargetKind() != LabelTargetMessage {
		t.Errorf("kind=%s want message", a.TargetKind())
	}
	if a.MessageID() != "msg_01" {
		t.Errorf("messageID mismatch: %q", a.MessageID())
	}
	if a.Chat() != jid {
		t.Errorf("message assignment must also carry chat")
	}

	if _, err := NewMessageLabelAssignment("lbl", jid, "", now); err == nil {
		t.Error("want error on empty messageID")
	}
}

func TestLabelTargetKind(t *testing.T) {
	if !LabelTargetChat.IsValid() || !LabelTargetMessage.IsValid() {
		t.Error("declared kinds must be valid")
	}
	if LabelTargetKind(0).IsValid() {
		t.Error("zero kind must be invalid")
	}
	if LabelTargetChat.String() != "chat" || LabelTargetMessage.String() != "message" {
		t.Errorf("string mismatch")
	}
}
