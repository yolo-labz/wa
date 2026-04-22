package domain

import "testing"

// TestAuditKindStableStrings asserts the wire strings of the Tier-2
// audit kinds — audit.log is append-only forensic evidence; renaming a
// kind silently breaks downstream tooling. Breaking this test requires
// an explicit migration note in plan-tier2.md.
func TestAuditKindStableStrings(t *testing.T) {
	t.Parallel()
	cases := []struct {
		a    AuditAction
		want string
	}{
		{AuditEdit, "edit"},
		{AuditBlock, "block"},
		{AuditUnblock, "unblock"},
		{AuditLeaveGroup, "leave_group"},
		{AuditPrivacyChange, "privacy_change"},
		{AuditLogoutAll, "logout_all"},
		{AuditGroupCreate, "group_create"},
		{AuditGroupParticipantsPatch, "group_participants_patch"},
		{AuditGroupInviteJoin, "group_invite_join"},
		{AuditDisappearingSet, "disappearing_set"},
		{AuditProfileEdit, "profile_edit"},
	}
	seen := map[string]bool{}
	for _, c := range cases {
		got := c.a.String()
		if got != c.want {
			t.Errorf("%d: got %q want %q", c.a, got, c.want)
		}
		if seen[got] {
			t.Errorf("duplicate wire tag %q", got)
		}
		seen[got] = true
	}
	if len(seen) != 11 {
		t.Errorf("want 11 Tier-2 audit kinds, got %d", len(seen))
	}
}

// TestAuditKindOrdinalsStable pins the integer values of the new kinds
// so that audit.log rows persisted with a numeric kind (if tooling ever
// chooses to) remain readable across versions.
func TestAuditKindOrdinalsStable(t *testing.T) {
	t.Parallel()
	cases := map[AuditAction]uint8{
		AuditEdit:                   11,
		AuditBlock:                  12,
		AuditUnblock:                13,
		AuditLeaveGroup:             14,
		AuditPrivacyChange:          15,
		AuditLogoutAll:              16,
		AuditGroupCreate:            17,
		AuditGroupParticipantsPatch: 18,
		AuditGroupInviteJoin:        19,
		AuditDisappearingSet:        20,
		AuditProfileEdit:            21,
	}
	for kind, want := range cases {
		if uint8(kind) != want {
			t.Errorf("%s: got ordinal %d want %d", kind, uint8(kind), want)
		}
	}
}
