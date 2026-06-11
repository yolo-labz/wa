package porttest

import (
	"context"
	"testing"

	"github.com/yolo-labz/wa/v2/internal/app"
	"github.com/yolo-labz/wa/v2/internal/domain"
)

// GroupManagerHarness couples the GroupManager port with the test-only
// seed hook the contract clauses use to install groups without reaching
// into adapter internals.
type GroupManagerHarness interface {
	app.GroupManager
	SeedGroup(g domain.Group)
}

// GroupManagerFactory returns a fresh harness for one sub-test.
type GroupManagerFactory func(t *testing.T) GroupManagerHarness

// testGroupManager adapts the suite-wide Factory to the standalone
// runner; the clauses live in RunGroupManagerContract.
func testGroupManager(t *testing.T, factory Factory) {
	t.Helper()
	RunGroupManagerContract(t, func(t *testing.T) GroupManagerHarness { return factory(t) })
}

// RunGroupManagerContract exercises the list/get clauses against any
// GroupManager implementation. Standalone runner per the registry.go
// convention (018 audit TEST-04): adapters that implement only this
// port don't need the full RunContractSuite Adapter surface.
func RunGroupManagerContract(t *testing.T, factory GroupManagerFactory) {
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
