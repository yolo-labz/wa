package porttest

import (
	"context"
	"testing"

	"github.com/yolo-labz/wa/v2/internal/app"
	"github.com/yolo-labz/wa/v2/internal/domain"
)

// ContactDirectoryHarness couples the ContactDirectory port with the
// test-only seed hook the contract clauses use to install contacts
// without reaching into adapter internals.
type ContactDirectoryHarness interface {
	app.ContactDirectory
	SeedContact(c domain.Contact)
}

// ContactDirectoryFactory returns a fresh harness for one sub-test.
type ContactDirectoryFactory func(t *testing.T) ContactDirectoryHarness

// testContactDirectory adapts the suite-wide Factory to the standalone
// runner; the clauses live in RunContactDirectoryContract.
func testContactDirectory(t *testing.T, factory Factory) {
	t.Helper()
	RunContactDirectoryContract(t, func(t *testing.T) ContactDirectoryHarness { return factory(t) })
}

// RunContactDirectoryContract exercises the lookup/resolve clauses
// against any ContactDirectory implementation. Standalone runner per
// the registry.go convention (018 audit TEST-04): adapters that
// implement only this port don't need the full RunContractSuite
// Adapter surface.
func RunContactDirectoryContract(t *testing.T, factory ContactDirectoryFactory) {
	t.Helper()
	alice := domain.MustJID("5511999999999")

	t.Run("lookup_found", func(t *testing.T) {
		a := factory(t)
		c, _ := domain.NewContact(alice, "Alice")
		a.SeedContact(c)
		got, err := a.Lookup(context.Background(), alice)
		if err != nil {
			reportf(t, "ContactDirectory", "Lookup", "found", "nil error", err.Error())
		}
		if got.PushName != "Alice" {
			reportf(t, "ContactDirectory", "Lookup", "found", "Alice", got.PushName)
		}
	})

	t.Run("lookup_not_found", func(t *testing.T) {
		a := factory(t)
		_, err := a.Lookup(context.Background(), alice)
		if err == nil {
			reportf(t, "ContactDirectory", "Lookup", "not_found", "typed error", "nil")
		}
	})

	t.Run("resolve_valid_phone", func(t *testing.T) {
		a := factory(t)
		got, err := a.Resolve(context.Background(), "+55 (11) 99999-9999")
		if err != nil {
			reportf(t, "ContactDirectory", "Resolve", "valid", "nil error", err.Error())
		}
		if got != alice {
			reportf(t, "ContactDirectory", "Resolve", "valid", alice.String(), got.String())
		}
	})

	t.Run("resolve_malformed", func(t *testing.T) {
		a := factory(t)
		_, err := a.Resolve(context.Background(), "not-a-phone")
		if err == nil {
			reportf(t, "ContactDirectory", "Resolve", "malformed", "error", "nil")
		}
	})
}
