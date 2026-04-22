package porttest

import (
	"sort"
	"testing"
)

// TestRegistrySorted guards the canonical-order invariant: entries are
// listed alphabetically so diffs stay minimal when ports are added.
func TestRegistrySorted(t *testing.T) {
	if !sort.StringsAreSorted(Registry) {
		t.Fatalf("Registry is not sorted; fix ordering in registry.go")
	}
}

// TestRegistryNoDupes guards against accidental double-entries.
func TestRegistryNoDupes(t *testing.T) {
	seen := make(map[string]struct{}, len(Registry))
	for _, name := range Registry {
		if _, ok := seen[name]; ok {
			t.Errorf("duplicate entry in Registry: %q", name)
		}
		seen[name] = struct{}{}
	}
}
