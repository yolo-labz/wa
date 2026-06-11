package porttest

import (
	"sync"
	"testing"

	"github.com/yolo-labz/wa/v2/internal/app"
	"github.com/yolo-labz/wa/v2/internal/domain"
)

// AllowlistHarness couples the Allowlist port with the grant/revoke
// surface the contract clauses use to seed policy. The port itself is
// read-only by design (Allows is the only production method); the
// canonical implementation is *domain.Allowlist, which satisfies this
// harness trivially.
type AllowlistHarness interface {
	app.Allowlist
	Grant(jid domain.JID, actions ...domain.Action)
	Revoke(jid domain.JID, actions ...domain.Action)
}

// AllowlistFactory returns a fresh harness for one sub-test.
type AllowlistFactory func(t *testing.T) AllowlistHarness

// allowlistMutator is the grant/revoke surface the suite-wide Adapter
// is expected to expose alongside the read-only port. testAllowlistPort
// type-asserts it once and folds both into an AllowlistHarness.
type allowlistMutator interface {
	Grant(jid domain.JID, actions ...domain.Action)
	Revoke(jid domain.JID, actions ...domain.Action)
}

// testAllowlistPort adapts the suite-wide Factory to the standalone
// runner; the clauses live in RunAllowlistContract.
func testAllowlistPort(t *testing.T, factory Factory) {
	t.Helper()
	RunAllowlistContract(t, func(t *testing.T) AllowlistHarness {
		a := factory(t)
		m, ok := a.(allowlistMutator)
		if !ok {
			t.Fatalf("adapter does not expose Grant/Revoke; cannot seed grants")
		}
		return struct {
			app.Allowlist
			allowlistMutator
		}{a, m}
	})
}

// RunAllowlistContract exercises the AL1–AL6 clauses against any
// Allowlist implementation. Standalone runner per the registry.go
// convention (018 audit TEST-04): adapters that implement only this
// port don't need the full RunContractSuite Adapter surface.
func RunAllowlistContract(t *testing.T, factory AllowlistFactory) {
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
		a.Grant(jid, domain.ActionRead)
		if !a.Allows(jid, domain.ActionRead) {
			reportf(t, "Allowlist", "Allows", "AL2", "true", "false")
		}
	})

	t.Run("AL3_no_promotion", func(t *testing.T) {
		a := factory(t)
		a.Grant(jid, domain.ActionRead)
		if a.Allows(jid, domain.ActionSend) {
			reportf(t, "Allowlist", "Allows", "AL3", "false", "true")
		}
	})

	t.Run("AL4_grant_then_revoke", func(t *testing.T) {
		a := factory(t)
		a.Grant(jid, domain.ActionSend)
		a.Revoke(jid, domain.ActionSend)
		if a.Allows(jid, domain.ActionSend) {
			reportf(t, "Allowlist", "Allows", "AL4", "false", "true")
		}
	})

	t.Run("AL5_parallel_reads", func(t *testing.T) {
		a := factory(t)
		a.Grant(jid, domain.ActionRead)
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
