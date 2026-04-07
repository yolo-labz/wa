package porttest

import (
	"context"
	"testing"

	"github.com/yolo-labz/wa/internal/domain"
)

func testContactDirectory(t *testing.T, factory Factory) {
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
