package domain

import (
	"sync"
	"testing"
)

func TestAllowlist_EmptyDenies(t *testing.T) {
	t.Parallel()
	a := NewAllowlist()
	if a.Allows(testRecipient, ActionSend) {
		t.Error("empty allowlist must default-deny")
	}
	if a.Size() != 0 {
		t.Errorf("Size=%d", a.Size())
	}
}

func TestAllowlist_GrantAllow(t *testing.T) {
	t.Parallel()
	a := NewAllowlist()
	a.Grant(testRecipient, ActionRead)
	if !a.Allows(testRecipient, ActionRead) {
		t.Error("Grant then Allows should be true")
	}
}

func TestAllowlist_NoImplicitPromotion(t *testing.T) {
	t.Parallel()
	a := NewAllowlist()
	a.Grant(testRecipient, ActionRead)
	if a.Allows(testRecipient, ActionSend) {
		t.Error("Grant Read must not imply Send")
	}
}

func TestAllowlist_Revoke(t *testing.T) {
	t.Parallel()
	a := NewAllowlist()
	a.Grant(testRecipient, ActionSend)
	a.Revoke(testRecipient, ActionSend)
	if a.Allows(testRecipient, ActionSend) {
		t.Error("revoked action still allowed")
	}
}

func TestAllowlist_PartialRevoke(t *testing.T) {
	t.Parallel()
	a := NewAllowlist()
	a.Grant(testRecipient, ActionRead, ActionSend)
	a.Revoke(testRecipient, ActionRead)
	if a.Allows(testRecipient, ActionRead) {
		t.Error("revoked Read still allowed")
	}
	if !a.Allows(testRecipient, ActionSend) {
		t.Error("Send should still be allowed")
	}
}

// TestAllowlist_GrantNewJID verifies Grant on a previously-unknown JID
// correctly persists the entry.  Regression test for C-004.
func TestAllowlist_GrantNewJID(t *testing.T) {
	t.Parallel()
	a := NewAllowlist()
	newJID := MustJID("5511222333444@s.whatsapp.net")
	a.Grant(newJID, ActionRead, ActionSend)
	if !a.Allows(newJID, ActionRead) {
		t.Error("Grant on new JID should persist Read")
	}
	if !a.Allows(newJID, ActionSend) {
		t.Error("Grant on new JID should persist Send")
	}
	if a.Size() != 1 {
		t.Errorf("Size=%d, want 1", a.Size())
	}
}

func TestAllowlist_GrantZeroNoop(t *testing.T) {
	t.Parallel()
	a := NewAllowlist()
	var zero Action
	a.Grant(testRecipient, zero)
	if a.Size() != 0 {
		t.Errorf("Size=%d after zero-action grant", a.Size())
	}
}

func TestAllowlist_EntriesDefensiveCopy(t *testing.T) {
	t.Parallel()
	a := NewAllowlist()
	a.Grant(testRecipient, ActionRead)
	m := a.Entries()
	delete(m, testRecipient)
	if !a.Allows(testRecipient, ActionRead) {
		t.Error("Entries() mutation leaked into Allowlist")
	}
}

// TestAllowlist_PNLIDNamespacesDistinct pins spec 105 FR-009: granting
// `send` to a phone-number JID MUST NOT authorise the LID with the
// same digit string (and vice versa). The `Allowlist.entries` map
// keys on the full `(user, server)` tuple via Go struct equality, so
// PN and LID with identical digits hash to distinct buckets — this
// test makes that invariant a load-bearing contract instead of an
// implementation detail.
func TestAllowlist_PNLIDNamespacesDistinct(t *testing.T) {
	t.Parallel()
	pn := MustJID("66448177246461@s.whatsapp.net")
	lid := MustJID("66448177246461@lid")
	if pn == lid {
		t.Fatal("test fixture invalid: PN and LID with same digits must compare unequal")
	}
	a := NewAllowlist()
	a.Grant(pn, ActionSend)
	if !a.Allows(pn, ActionSend) {
		t.Error("PN grant should authorise PN send")
	}
	if a.Allows(lid, ActionSend) {
		t.Error("regression: PN grant must NOT authorise LID send (spec 105 FR-009)")
	}
	a.Grant(lid, ActionSend)
	if a.Size() != 2 {
		t.Errorf("Size=%d, want 2 (PN and LID are distinct allowlist entries)", a.Size())
	}
	a.Revoke(pn, ActionSend)
	if a.Allows(pn, ActionSend) {
		t.Error("PN revoke did not take effect")
	}
	if !a.Allows(lid, ActionSend) {
		t.Error("PN revoke leaked into LID — namespaces must be independent")
	}
}

func TestAllowlist_ParallelRace(t *testing.T) {
	t.Parallel()
	a := NewAllowlist()
	a.Grant(testRecipient, ActionRead)
	var wg sync.WaitGroup
	// readers
	for range 8 {
		wg.Go(func() {
			for range 1000 {
				_ = a.Allows(testRecipient, ActionRead)
			}
		})
	}
	// writers
	for range 4 {
		wg.Go(func() {
			for range 100 {
				a.Grant(testRecipient, ActionSend)
				a.Revoke(testRecipient, ActionSend)
			}
		})
	}
	wg.Wait()
}
