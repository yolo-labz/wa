package domain

import (
	"errors"
	"strings"
	"testing"
)

func TestNewGroup_Happy(t *testing.T) {
	t.Parallel()
	g, err := NewGroup(
		MustJID("120363042199654321@g.us"),
		"Test",
		[]JID{MustJID("5511999999999"), MustJID("5511888888888")},
	)
	if err != nil {
		t.Fatal(err)
	}
	if g.Size() != 2 {
		t.Errorf("Size=%d", g.Size())
	}
	if !g.HasParticipant(MustJID("5511999999999")) {
		t.Error("HasParticipant false")
	}
	if g.IsAdmin(MustJID("5511999999999")) {
		t.Error("admins should be empty")
	}
}

func TestNewGroup_NonGroupJID(t *testing.T) {
	t.Parallel()
	_, err := NewGroup(MustJID("5511999999999"), "X", []JID{MustJID("5511888888888")})
	if !errors.Is(err, ErrInvalidJID) {
		t.Errorf("want ErrInvalidJID, got %v", err)
	}
}

func TestNewGroup_OversizeSubject(t *testing.T) {
	t.Parallel()
	_, err := NewGroup(
		MustJID("120363042199654321@g.us"),
		strings.Repeat("x", 101),
		[]JID{MustJID("5511999999999")},
	)
	if !errors.Is(err, ErrMessageTooLarge) {
		t.Errorf("want ErrMessageTooLarge, got %v", err)
	}
}

func TestNewGroup_GroupParticipantRejected(t *testing.T) {
	t.Parallel()
	_, err := NewGroup(
		MustJID("120363042199654321@g.us"),
		"Test",
		[]JID{MustJID("120363000000000000@g.us")},
	)
	if !errors.Is(err, ErrInvalidJID) {
		t.Errorf("want ErrInvalidJID for nested group, got %v", err)
	}
}

func TestNewGroup_EmptyParticipants(t *testing.T) {
	t.Parallel()
	_, err := NewGroup(MustJID("120363042199654321@g.us"), "Test", nil)
	if !errors.Is(err, ErrInvalidJID) {
		t.Errorf("want ErrInvalidJID for empty, got %v", err)
	}
}

func TestGroup_IsAdmin(t *testing.T) {
	t.Parallel()
	alice := MustJID("5511999999999")
	g, _ := NewGroup(MustJID("120363042199654321@g.us"), "X", []JID{alice})
	g.Admins = []JID{alice}
	if !g.IsAdmin(alice) {
		t.Error("IsAdmin false")
	}
}
