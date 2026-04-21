package domain

import (
	"errors"
	"strings"
	"testing"
	"time"
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

// TestGroupAdminFlagsRoundTrip exercises WithAdmins + IsAdmin and the
// non-participant rejection rule (FR-072).
func TestGroupAdminFlagsRoundTrip(t *testing.T) {
	t.Parallel()
	alice := MustJID("5511999999999")
	bob := MustJID("5511888888888")
	outsider := MustJID("5511111111111")

	g, err := NewGroup(
		MustJID("120363042199654321@g.us"),
		"T",
		[]JID{alice, bob},
	)
	if err != nil {
		t.Fatal(err)
	}

	promoted, err := g.WithAdmins([]JID{alice})
	if err != nil {
		t.Fatalf("WithAdmins: %v", err)
	}
	if !promoted.IsAdmin(alice) {
		t.Error("alice should be admin after promote")
	}
	if promoted.IsAdmin(bob) {
		t.Error("bob should not be admin")
	}
	if g.IsAdmin(alice) {
		t.Error("original group mutated — WithAdmins must return a copy")
	}

	if _, err := g.WithAdmins([]JID{outsider}); !errors.Is(err, ErrInvalidJID) {
		t.Errorf("want ErrInvalidJID for non-participant admin, got %v", err)
	}
}

// TestGroup_IsStale checks FetchedAt TTL semantics (FR-073 cache freshness).
func TestGroup_IsStale(t *testing.T) {
	t.Parallel()
	alice := MustJID("5511999999999")
	g, _ := NewGroup(MustJID("120363042199654321@g.us"), "X", []JID{alice})

	now := time.Date(2026, 4, 20, 12, 0, 0, 0, time.UTC)

	if !g.IsStale(now, 60*time.Second) {
		t.Error("zero FetchedAt must be stale")
	}

	g.FetchedAt = now.Add(-30 * time.Second)
	if g.IsStale(now, 60*time.Second) {
		t.Error("30s-old FetchedAt with 60s TTL should be fresh")
	}

	g.FetchedAt = now.Add(-120 * time.Second)
	if !g.IsStale(now, 60*time.Second) {
		t.Error("120s-old FetchedAt with 60s TTL should be stale")
	}
}
