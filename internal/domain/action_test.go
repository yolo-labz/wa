package domain

import (
	"errors"
	"testing"
)

func TestAction_StringAndValid(t *testing.T) {
	t.Parallel()
	cases := []struct {
		a    Action
		name string
	}{
		{ActionRead, "read"},
		{ActionSend, "send"},
		{ActionGroupAdd, "group.add"},
		{ActionGroupCreate, "group.create"},
	}
	for _, c := range cases {
		if c.a.String() != c.name {
			t.Errorf("String(%d)=%q want %q", c.a, c.a.String(), c.name)
		}
		if !c.a.IsValid() {
			t.Errorf("%v should be valid", c.a)
		}
	}
}

func TestAction_ZeroInvalid(t *testing.T) {
	t.Parallel()
	var a Action
	if a.IsValid() {
		t.Error("zero Action must not be valid")
	}
}

func TestParseAction_RoundTrip(t *testing.T) {
	t.Parallel()
	for _, a := range []Action{ActionRead, ActionSend, ActionGroupAdd, ActionGroupCreate} {
		got, err := ParseAction(a.String())
		if err != nil {
			t.Errorf("ParseAction(%q): %v", a.String(), err)
		}
		if got != a {
			t.Errorf("round-trip: got %v want %v", got, a)
		}
	}
}

func TestParseAction_Unknown(t *testing.T) {
	t.Parallel()
	_, err := ParseAction("nope")
	if !errors.Is(err, ErrUnknownAction) {
		t.Errorf("want ErrUnknownAction, got %v", err)
	}
}
