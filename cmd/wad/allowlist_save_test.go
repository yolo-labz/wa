package main

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/yolo-labz/wa/v2/internal/domain"
)

// mustParseJID is a test helper that parses a canonical JID or fails the
// test. Keeps the round-trip table readable.
func mustParseJID(t *testing.T, s string) domain.JID {
	t.Helper()
	jid, err := domain.Parse(s)
	if err != nil {
		t.Fatalf("domain.Parse(%q): %v", s, err)
	}
	return jid
}

// TestSaveAllowlistRoundTrip is the cheapest, highest-value gap closure:
// it asserts loadAllowlist(saveAllowlist(al)) == al for a populated
// allowlist spanning multiple JIDs and multiple actions per JID. The
// TOML schema (allowlistFile/allowlistRule) carries jid + actions only —
// there is no comment field — so equality is over the JID→[]Action map
// that domain.Allowlist.Entries() returns.
//
// Entries() returns actions in ordinal-sorted order (actionSet.list),
// so a direct reflect.DeepEqual of the two entry maps is deterministic.
func TestSaveAllowlistRoundTrip(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name  string
		grant map[string][]domain.Action
	}{
		{
			name: "single jid single action",
			grant: map[string][]domain.Action{
				"15551234567@s.whatsapp.net": {domain.ActionSend},
			},
		},
		{
			name: "multiple jids multiple actions",
			grant: map[string][]domain.Action{
				"15551234567@s.whatsapp.net": {domain.ActionSend, domain.ActionRead},
				"15559998888@s.whatsapp.net": {domain.ActionRead, domain.ActionGroupCreate, domain.ActionGroupAdd},
				"120363011112222333@g.us":    {domain.ActionGroupEdit, domain.ActionGroupInvite},
			},
		},
		{
			name: "tier-2 action surface coverage",
			grant: map[string][]domain.Action{
				"15551112222@s.whatsapp.net": {
					domain.ActionRevoke, domain.ActionEdit, domain.ActionBlock,
					domain.ActionPrivacySet, domain.ActionProfileEdit, domain.ActionLogout,
				},
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			al := domain.NewAllowlist()
			for jidStr, actions := range tc.grant {
				al.Grant(mustParseJID(t, jidStr), actions...)
			}

			path := filepath.Join(t.TempDir(), "allowlist.toml")
			if err := saveAllowlist(path, al); err != nil {
				t.Fatalf("saveAllowlist: %v", err)
			}

			// The temp file must be gone (write-then-rename leaves no .tmp).
			if _, err := os.Stat(path + ".tmp"); !os.IsNotExist(err) {
				t.Errorf("saveAllowlist left a .tmp file behind: %v", err)
			}

			got, err := loadAllowlist(path)
			if err != nil {
				t.Fatalf("loadAllowlist: %v", err)
			}

			if got.Size() != al.Size() {
				t.Fatalf("round-trip Size() = %d, want %d", got.Size(), al.Size())
			}
			if !reflect.DeepEqual(got.Entries(), al.Entries()) {
				t.Errorf("round-trip Entries() mismatch:\n got  = %v\n want = %v",
					got.Entries(), al.Entries())
			}
		})
	}
}

// TestSaveAllowlistEmpty asserts the degenerate empty-allowlist case
// round-trips to an empty allowlist rather than erroring or producing a
// file the loader rejects.
func TestSaveAllowlistEmpty(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "allowlist.toml")
	if err := saveAllowlist(path, domain.NewAllowlist()); err != nil {
		t.Fatalf("saveAllowlist(empty): %v", err)
	}

	got, err := loadAllowlist(path)
	if err != nil {
		t.Fatalf("loadAllowlist: %v", err)
	}
	if got.Size() != 0 {
		t.Errorf("empty round-trip Size() = %d, want 0", got.Size())
	}
}

// TestSaveAllowlistOverwrite asserts the atomic rename overwrites an
// existing target file in place — a second save with different content
// fully replaces the first (no stale rules survive).
func TestSaveAllowlistOverwrite(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "allowlist.toml")

	first := domain.NewAllowlist()
	first.Grant(mustParseJID(t, "15551234567@s.whatsapp.net"), domain.ActionSend)
	if err := saveAllowlist(path, first); err != nil {
		t.Fatalf("saveAllowlist(first): %v", err)
	}

	second := domain.NewAllowlist()
	second.Grant(mustParseJID(t, "15559998888@s.whatsapp.net"), domain.ActionRead)
	if err := saveAllowlist(path, second); err != nil {
		t.Fatalf("saveAllowlist(second): %v", err)
	}

	got, err := loadAllowlist(path)
	if err != nil {
		t.Fatalf("loadAllowlist: %v", err)
	}
	if !reflect.DeepEqual(got.Entries(), second.Entries()) {
		t.Errorf("overwrite did not replace content:\n got  = %v\n want = %v",
			got.Entries(), second.Entries())
	}
}
