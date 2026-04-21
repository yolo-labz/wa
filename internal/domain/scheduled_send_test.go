package domain

import (
	"errors"
	"testing"
	"time"
)

func schedNow() time.Time { return time.Date(2026, 4, 20, 12, 0, 0, 0, time.UTC) }

func newPending(t *testing.T) ScheduledSend {
	t.Helper()
	s, err := NewScheduledSend("sch-1", "default", ScheduleKindSendText, MustJID("5511999999999"), "hi", "", schedNow().Add(time.Hour), schedNow())
	if err != nil {
		t.Fatalf("NewScheduledSend: %v", err)
	}
	return s
}

func TestScheduleStateTransitions(t *testing.T) {
	t.Run("pending_to_fired", func(t *testing.T) {
		s := newPending(t)
		if s.State() != SchedulePending {
			t.Fatalf("state = %s, want pending", s.State())
		}
		fired, err := s.MarkFired(schedNow().Add(time.Hour))
		if err != nil {
			t.Fatalf("MarkFired: %v", err)
		}
		if fired.State() != ScheduleFired {
			t.Fatalf("state = %s, want fired", fired.State())
		}
		if !fired.State().IsTerminal() {
			t.Fatalf("fired should be terminal")
		}
	})

	t.Run("pending_to_cancelled", func(t *testing.T) {
		s := newPending(t)
		c, err := s.MarkCancelled(schedNow())
		if err != nil {
			t.Fatalf("MarkCancelled: %v", err)
		}
		if c.State() != ScheduleCancelled {
			t.Fatalf("state = %s", c.State())
		}
	})

	t.Run("pending_to_failed", func(t *testing.T) {
		s := newPending(t)
		f, err := s.MarkFailed(schedNow(), "rate_limited")
		if err != nil {
			t.Fatalf("MarkFailed: %v", err)
		}
		if f.State() != ScheduleFailed || f.LastError() != "rate_limited" {
			t.Fatalf("state=%s last_error=%q", f.State(), f.LastError())
		}
	})

	t.Run("double_transition_rejected", func(t *testing.T) {
		s, _ := newPending(t).MarkFired(schedNow())
		if _, err := s.MarkCancelled(schedNow()); !errors.Is(err, ErrScheduleTerminal) {
			t.Fatalf("want ErrScheduleTerminal, got %v", err)
		}
	})

	t.Run("reject_past_fire_at", func(t *testing.T) {
		if _, err := NewScheduledSend("x", "default", ScheduleKindSendText, MustJID("5511999999999"), "hi", "", schedNow(), schedNow()); !errors.Is(err, ErrScheduleInPast) {
			t.Fatalf("want ErrScheduleInPast, got %v", err)
		}
	})

	t.Run("reschedule_pending_ok", func(t *testing.T) {
		s := newPending(t)
		later := schedNow().Add(2 * time.Hour)
		s2, err := s.Reschedule(later, schedNow())
		if err != nil {
			t.Fatalf("Reschedule: %v", err)
		}
		if !s2.FireAt().Equal(later.UTC()) {
			t.Fatalf("fire_at not updated: %v", s2.FireAt())
		}
	})

	t.Run("reschedule_terminal_rejected", func(t *testing.T) {
		s, _ := newPending(t).MarkFired(schedNow())
		if _, err := s.Reschedule(schedNow().Add(time.Hour), schedNow()); !errors.Is(err, ErrScheduleTerminal) {
			t.Fatalf("want ErrScheduleTerminal, got %v", err)
		}
	})

	t.Run("text_rejects_empty_body", func(t *testing.T) {
		if _, err := NewScheduledSend("x", "default", ScheduleKindSendText, MustJID("5511999999999"), "", "", schedNow().Add(time.Hour), schedNow()); !errors.Is(err, ErrEmptyBody) {
			t.Fatalf("want ErrEmptyBody, got %v", err)
		}
	})

	t.Run("media_rejects_missing_path", func(t *testing.T) {
		if _, err := NewScheduledSend("x", "default", ScheduleKindSendMedia, MustJID("5511999999999"), "cap", "", schedNow().Add(time.Hour), schedNow()); err == nil {
			t.Fatalf("want error for missing media path")
		}
	})
}
