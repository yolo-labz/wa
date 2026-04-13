package porttest

import (
	"sync"
	"testing"

	"github.com/yolo-labz/wa/internal/domain"
)

func testAllowlistPort(t *testing.T, factory Factory) {
	t.Helper()
	jid := domain.MustJID("5511999999999")

	t.Run("AL1_empty_default_deny", func(t *testing.T) {
		a := factory(t)
		if a.Allows(jid, domain.ActionRead) {
			reportf(t, "Allowlist", "Allows", "AL1", "false", "true")
		}
	})

	t.Run("AL2_grant_read", func(t *testing.T) {
		a := factory(t)
		grantOn(t, a, jid, domain.ActionRead)
		if !a.Allows(jid, domain.ActionRead) {
			reportf(t, "Allowlist", "Allows", "AL2", "true", "false")
		}
	})

	t.Run("AL3_no_promotion", func(t *testing.T) {
		a := factory(t)
		grantOn(t, a, jid, domain.ActionRead)
		if a.Allows(jid, domain.ActionSend) {
			reportf(t, "Allowlist", "Allows", "AL3", "false", "true")
		}
	})

	t.Run("AL4_grant_then_revoke", func(t *testing.T) {
		a := factory(t)
		grantOn(t, a, jid, domain.ActionSend)
		revokeOn(t, a, jid, domain.ActionSend)
		if a.Allows(jid, domain.ActionSend) {
			reportf(t, "Allowlist", "Allows", "AL4", "false", "true")
		}
	})

	t.Run("AL5_parallel_reads", func(t *testing.T) {
		a := factory(t)
		grantOn(t, a, jid, domain.ActionRead)
		var wg sync.WaitGroup
		for range 8 {
			wg.Go(func() {
				for range 1000 {
					_ = a.Allows(jid, domain.ActionRead)
				}
			})
		}
		wg.Wait()
	})

	t.Run("AL6_unknown_denied", func(t *testing.T) {
		a := factory(t)
		other := domain.MustJID("5511888888888")
		if a.Allows(other, domain.ActionRead) {
			reportf(t, "Allowlist", "Allows", "AL6", "false", "true")
		}
	})
}

// grantOn is a helper: the Allowlist *port* does not declare Grant,
// because the canonical implementation is *domain.Allowlist. The test
// reaches into the adapter via a type assertion to access the underlying
// grant surface. Adapters that ship the domain Allowlist as-is satisfy
// this trivially.
type allowlistMutator interface {
	Grant(jid domain.JID, actions ...domain.Action)
	Revoke(jid domain.JID, actions ...domain.Action)
}

func grantOn(t *testing.T, a Adapter, jid domain.JID, act domain.Action) {
	t.Helper()
	m, ok := a.(allowlistMutator)
	if !ok {
		t.Fatalf("adapter does not expose allowlistMutator; cannot seed grants")
	}
	m.Grant(jid, act)
}

func revokeOn(t *testing.T, a Adapter, jid domain.JID, act domain.Action) {
	t.Helper()
	m, ok := a.(allowlistMutator)
	if !ok {
		t.Fatalf("adapter does not expose allowlistMutator; cannot seed revokes")
	}
	m.Revoke(jid, act)
}
