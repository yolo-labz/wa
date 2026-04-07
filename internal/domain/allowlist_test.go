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

func TestAllowlist_ParallelRace(t *testing.T) {
	t.Parallel()
	a := NewAllowlist()
	a.Grant(testRecipient, ActionRead)
	var wg sync.WaitGroup
	// readers
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 1000; j++ {
				_ = a.Allows(testRecipient, ActionRead)
			}
		}()
	}
	// writers
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				a.Grant(testRecipient, ActionSend)
				a.Revoke(testRecipient, ActionSend)
			}
		}()
	}
	wg.Wait()
}
