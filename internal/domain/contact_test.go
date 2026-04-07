package domain

import (
	"errors"
	"testing"
)

func TestNewContact_Happy(t *testing.T) {
	t.Parallel()
	c, err := NewContact(MustJID("5511999999999"), "Alice")
	if err != nil {
		t.Fatal(err)
	}
	if c.DisplayName() != "Alice" {
		t.Errorf("DisplayName=%q", c.DisplayName())
	}
}

func TestNewContact_ZeroJID(t *testing.T) {
	t.Parallel()
	_, err := NewContact(JID{}, "Alice")
	if !errors.Is(err, ErrInvalidJID) {
		t.Errorf("want ErrInvalidJID, got %v", err)
	}
}

func TestContact_DisplayNameFallback(t *testing.T) {
	t.Parallel()
	c, err := NewContact(MustJID("5511999999999"), "")
	if err != nil {
		t.Fatal(err)
	}
	if c.DisplayName() != "5511999999999" {
		t.Errorf("want fallback to user, got %q", c.DisplayName())
	}
}

func TestContact_IsZero(t *testing.T) {
	t.Parallel()
	var c Contact
	if !c.IsZero() {
		t.Error("zero Contact should be IsZero")
	}
}
