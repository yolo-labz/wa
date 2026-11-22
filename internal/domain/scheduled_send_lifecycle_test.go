package domain

import (
	"errors"
	"testing"
	"time"
)

func TestScheduleEnumStrings(t *testing.T) {
	t.Parallel()
	for s, want := range map[ScheduledState]string{SchedulePending: "pending", ScheduleFired: "fired", ScheduleCancelled: "cancelled", ScheduleFailed: "failed", 0: "unknown", 9: "unknown"} {
		if s.String() != want {
			t.Errorf("state %d = %q, want %q", s, s.String(), want)
		}
		if valid := want != "unknown"; s.IsValid() != valid {
			t.Errorf("state %d IsValid = %v, want %v", s, s.IsValid(), valid)
		}
		if terminal := want != "unknown" && s != SchedulePending; s.IsTerminal() != terminal {
			t.Errorf("state %d IsTerminal = %v, want %v", s, s.IsTerminal(), terminal)
		}
	}
	for k, want := range map[ScheduleKind]string{ScheduleKindSendText: "send_text", ScheduleKindSendMedia: "send_media", ScheduleKindCreateDraft: "create_draft", 0: "unknown", 7: "unknown"} {
		if k.String() != want || k.IsValid() != (want != "unknown") {
			t.Errorf("kind %d = %q valid=%v, want %q", k, k.String(), k.IsValid(), want)
		}
	}
}

// TestNewScheduledSend_Invariants: each invariant refuses on its own, and
// the accepted value is normalised to UTC.
func TestNewScheduledSend_Invariants(t *testing.T) {
	t.Parallel()
	now := schedNow()
	ok := ScheduledSendSpec{ID: "s", Profile: "p", Kind: ScheduleKindSendText, To: MustJID("5511999999999"), Body: "hi", FireAt: now.Add(time.Minute), Now: now}
	with := func(f func(*ScheduledSendSpec)) ScheduledSendSpec { s := ok; f(&s); return s }
	for name, tt := range map[string]struct {
		spec ScheduledSendSpec
		want error // nil = any error
	}{
		"empty id":           {with(func(s *ScheduledSendSpec) { s.ID = "" }), nil},
		"empty profile":      {with(func(s *ScheduledSendSpec) { s.Profile = "" }), nil},
		"invalid kind":       {with(func(s *ScheduledSendSpec) { s.Kind = 0 }), nil},
		"zero recipient":     {with(func(s *ScheduledSendSpec) { s.To = JID{} }), nil},
		"text without body":  {with(func(s *ScheduledSendSpec) { s.Body = "" }), ErrEmptyBody},
		"draft without body": {with(func(s *ScheduledSendSpec) { s.Kind, s.Body = ScheduleKindCreateDraft, "" }), ErrEmptyBody},
		"media without path": {with(func(s *ScheduledSendSpec) { s.Kind, s.Body = ScheduleKindSendMedia, "" }), nil},
		"fire_at equals now": {with(func(s *ScheduledSendSpec) { s.FireAt = now }), ErrScheduleInPast},
		"fire_at before now": {with(func(s *ScheduledSendSpec) { s.FireAt = now.Add(-time.Second) }), ErrScheduleInPast},
	} {
		_, err := NewScheduledSend(tt.spec)
		if err == nil || (tt.want != nil && !errors.Is(err, tt.want)) {
			t.Errorf("%s: err = %v, want %v", name, err, tt.want)
		}
	}
	// A media schedule needs a path, not a body.
	media := with(func(s *ScheduledSendSpec) { s.Kind, s.Body, s.MediaPath = ScheduleKindSendMedia, "", "/m/a.jpg" })
	if s, err := NewScheduledSend(media); err != nil || s.MediaPath() != "/m/a.jpg" || s.Body() != "" {
		t.Errorf("media schedule = %+v, %v", s, err)
	}
	local := time.FixedZone("BRT", -3*3600)
	s, err := NewScheduledSend(with(func(s *ScheduledSendSpec) { s.FireAt = now.Add(time.Hour).In(local); s.Now = now.In(local) }))
	if err != nil || s.FireAt().Location() != time.UTC || s.CreatedAt().Location() != time.UTC || s.State() != SchedulePending {
		t.Errorf("accepted = %+v, %v; want pending with UTC times", s, err)
	}
}

// TestScheduledSend_TerminalIsFinal: every transition out of a terminal
// state is refused and leaves the value untouched; transitions return a new
// value rather than mutating the receiver.
func TestScheduledSend_TerminalIsFinal(t *testing.T) {
	t.Parallel()
	later := schedNow().Add(2 * time.Hour)
	pending := newPending(t)
	fired, err := pending.MarkFired(later)
	if err != nil || pending.State() != SchedulePending || fired.State() != ScheduleFired || !fired.UpdatedAt().Equal(later) {
		t.Fatalf("MarkFired = %+v, %v; receiver state %s", fired, err, pending.State())
	}
	cancelled, _ := pending.MarkCancelled(later)
	failed, _ := pending.MarkFailed(later, "boom")
	for name, term := range map[string]ScheduledSend{"fired": fired, "cancelled": cancelled, "failed": failed} {
		for op, f := range map[string]func() (ScheduledSend, error){
			"MarkFired":     func() (ScheduledSend, error) { return term.MarkFired(later) },
			"MarkCancelled": func() (ScheduledSend, error) { return term.MarkCancelled(later) },
			"MarkFailed":    func() (ScheduledSend, error) { return term.MarkFailed(later, "x") },
			"Reschedule":    func() (ScheduledSend, error) { return term.Reschedule(later.Add(time.Hour), later) },
		} {
			got, err := f()
			if !errors.Is(err, ErrScheduleTerminal) || got.State() != term.State() || got.LastError() != term.LastError() {
				t.Errorf("%s.%s = state %s err %v; want refused and unchanged", name, op, got.State(), err)
			}
		}
	}
	if failed.LastError() != "boom" {
		t.Errorf("LastError = %q", failed.LastError())
	}
	if blank, _ := pending.MarkFailed(later, ""); blank.LastError() != "unspecified" {
		t.Errorf("blank reason recorded as %q, want unspecified", blank.LastError())
	}
}

func TestScheduledSend_Reschedule(t *testing.T) {
	t.Parallel()
	now := schedNow()
	s := newPending(t)
	if _, err := s.Reschedule(now, now); !errors.Is(err, ErrScheduleInPast) {
		t.Errorf("fire_at == now: %v, want ErrScheduleInPast", err)
	}
	next := now.Add(5 * time.Hour)
	moved, err := s.Reschedule(next, now.Add(time.Minute))
	if err != nil || !moved.FireAt().Equal(next) || !moved.UpdatedAt().Equal(now.Add(time.Minute)) || moved.State() != SchedulePending {
		t.Fatalf("Reschedule = %+v, %v", moved, err)
	}
	if !s.FireAt().Equal(now.Add(time.Hour)) {
		t.Error("Reschedule mutated the receiver")
	}
}

// TestHydrateScheduledSend_SkipsInvariants: a stored row keeps every field,
// including a past fire_at and a terminal state that NewScheduledSend would
// refuse (pending rows resumed after downtime).
func TestHydrateScheduledSend_SkipsInvariants(t *testing.T) {
	t.Parallel()
	at := schedNow()
	rec := ScheduledSendRecord{
		ID: "old", Profile: "p", Kind: ScheduleKindSendMedia, To: MustJID("5511999999999"), MediaPath: "/m/x",
		FireAt: at.Add(-48 * time.Hour), CreatedAt: at.Add(-72 * time.Hour), State: ScheduleFailed, UpdatedAt: at.Add(-47 * time.Hour), LastError: "gone",
	}
	s := HydrateScheduledSend(rec)
	if s.ID() != "old" || s.Profile() != "p" || s.Kind() != ScheduleKindSendMedia || s.Recipient() != rec.To || s.MediaPath() != "/m/x" || s.Body() != "" ||
		!s.FireAt().Equal(rec.FireAt) || !s.CreatedAt().Equal(rec.CreatedAt) || !s.UpdatedAt().Equal(rec.UpdatedAt) || s.State() != ScheduleFailed || s.LastError() != "gone" {
		t.Errorf("Hydrate lost a field: %+v", s)
	}
}
