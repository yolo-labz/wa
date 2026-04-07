package porttest

import (
	"context"
	"testing"

	"github.com/yolo-labz/wa/internal/domain"
)

func testGroupManager(t *testing.T, factory Factory) {
	t.Helper()
	gjid := domain.MustJID("120363042199654321@g.us")
	alice := domain.MustJID("5511999999999")

	t.Run("list_empty_non_nil", func(t *testing.T) {
		a := factory(t)
		gs, err := a.List(context.Background())
		if err != nil {
			reportf(t, "GroupManager", "List", "empty", "nil error", err.Error())
		}
		if gs == nil {
			reportf(t, "GroupManager", "List", "empty", "empty (non-nil) slice", "nil slice")
		}
		if len(gs) != 0 {
			reportf(t, "GroupManager", "List", "empty", "len 0", "nonzero")
		}
	})

	t.Run("list_one", func(t *testing.T) {
		a := factory(t)
		g, _ := domain.NewGroup(gjid, "Test", []domain.JID{alice})
		a.SeedGroup(g)
		gs, err := a.List(context.Background())
		if err != nil || len(gs) != 1 {
			reportf(t, "GroupManager", "List", "seeded", "len 1", "wrong")
		}
	})

	t.Run("get_found", func(t *testing.T) {
		a := factory(t)
		g, _ := domain.NewGroup(gjid, "Test", []domain.JID{alice})
		a.SeedGroup(g)
		got, err := a.Get(context.Background(), gjid)
		if err != nil {
			reportf(t, "GroupManager", "Get", "found", "nil error", err.Error())
		}
		if got.JID != gjid {
			reportf(t, "GroupManager", "Get", "found", gjid.String(), got.JID.String())
		}
	})

	t.Run("get_not_found", func(t *testing.T) {
		a := factory(t)
		_, err := a.Get(context.Background(), gjid)
		if err == nil {
			reportf(t, "GroupManager", "Get", "not_found", "error", "nil")
		}
	})
}
