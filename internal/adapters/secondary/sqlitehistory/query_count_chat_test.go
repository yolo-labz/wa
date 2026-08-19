package sqlitehistory_test

import (
	"context"
	"testing"
)

// TestCountChat covers the counter that lets export report how many rows a
// PN↔LID counterpart chat is hiding (issue #355). The load-bearing case is
// the unknown chat: it must count zero, not error, because export asks
// about a JID that may legitimately have never been seen and an advisory
// note must never fail the export it annotates.
func TestCountChat(t *testing.T) {
	t.Parallel()
	s := openTempStore(t)
	ctx := context.Background()

	const (
		chatPN  = "15551230001@s.whatsapp.net"
		chatLID = "50758024224979@lid"
	)
	insertAt(t, s, chatPN, "p100", 100)
	insertAt(t, s, chatPN, "p200", 200)
	insertAt(t, s, chatLID, "l150", 150)

	cases := []struct {
		name string
		chat string
		want int
	}{
		{"counts only the named chat", chatPN, 2},
		{"counts the linked half separately", chatLID, 1},
		{"unknown chat counts zero", "15559990002@s.whatsapp.net", 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := s.CountChat(ctx, tc.chat)
			if err != nil {
				t.Fatalf("CountChat(%s): %v", tc.chat, err)
			}
			if got != tc.want {
				t.Errorf("CountChat(%s) = %d, want %d", tc.chat, got, tc.want)
			}
		})
	}

	if _, err := s.CountChat(ctx, ""); err == nil {
		t.Error("CountChat(\"\") = nil error, want refusal on an empty JID")
	}
}
