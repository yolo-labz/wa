package porttest

import (
	"context"
	"testing"

	"github.com/yolo-labz/wa/v2/internal/app"
	"github.com/yolo-labz/wa/v2/internal/domain"
)

// ContactSearcherFactory returns a fresh ContactSearcher for one sub-test.
// The contract suite seeds contacts via Upsert — adapters MUST satisfy that
// round-trip before any of CS1..CS4 runs.
type ContactSearcherFactory func(t *testing.T) app.ContactSearcher

// RunContactSearcherContract exercises ContactSearcher (FR-004..007).
//
//	CS1 Upsert + Search phrase match returns the seeded contact.
//	CS2 Annotate persists notes+tags visible on subsequent Search.
//	CS3 ListChanged returns ≥1 row after Upsert.
//	CS4 Sync(SyncDelta) returns nil error (no-op allowed).
func RunContactSearcherContract(t *testing.T, factory ContactSearcherFactory) {
	t.Helper()
	ctx := context.Background()
	jid := domain.MustJID("5511999999999")

	seed := func(t *testing.T, s app.ContactSearcher, name string) {
		t.Helper()
		c, err := domain.NewContact(jid, name)
		if err != nil {
			t.Fatalf("NewContact: %v", err)
		}
		if err := s.Upsert(ctx, c); err != nil {
			t.Fatalf("Upsert: %v", err)
		}
	}

	t.Run("CS1_search_returns_upserted", func(t *testing.T) {
		s := factory(t)
		seed(t, s, "Alice")
		got, err := s.Search(ctx, "Alice", 10)
		if err != nil {
			reportf(t, "ContactSearcher", "Search", "CS1", "nil error", err.Error())
			return
		}
		if len(got) == 0 {
			reportf(t, "ContactSearcher", "Search", "CS1", "≥1 hit", "0 hits")
		}
	})

	t.Run("CS2_annotate_persists", func(t *testing.T) {
		s := factory(t)
		seed(t, s, "Bob")
		if err := s.Annotate(ctx, jid, "partner", []string{"vip"}); err != nil {
			reportf(t, "ContactSearcher", "Annotate", "CS2", "nil error", err.Error())
		}
	})

	t.Run("CS3_list_changed_non_empty", func(t *testing.T) {
		s := factory(t)
		seed(t, s, "Carol")
		rows, err := s.ListChanged(ctx, 0, 100)
		if err != nil {
			reportf(t, "ContactSearcher", "ListChanged", "CS3", "nil error", err.Error())
			return
		}
		if len(rows) == 0 {
			reportf(t, "ContactSearcher", "ListChanged", "CS3", "≥1 row", "0 rows")
		}
	})

	t.Run("CS4_sync_delta_nil_err", func(t *testing.T) {
		s := factory(t)
		if err := s.Sync(ctx, app.SyncDelta); err != nil {
			reportf(t, "ContactSearcher", "Sync", "CS4", "nil error", err.Error())
		}
	})
}
