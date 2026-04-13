package socket

import (
	"path/filepath"
	"strings"
	"testing"
)

// TestPathFor_ReturnsPerProfilePath asserts that PathFor(profile)
// returns a path that (a) is absolute, (b) contains the profile name
// with a .sock suffix, and (c) is under the platform's socket directory.
func TestPathFor_ReturnsPerProfilePath(t *testing.T) {
	for _, profile := range []string{"default", "work", "personal"} {
		t.Run(profile, func(t *testing.T) {
			p, err := PathFor(profile)
			if err != nil {
				t.Fatalf("PathFor(%q): %v", profile, err)
			}
			if !filepath.IsAbs(p) {
				t.Errorf("PathFor(%q) = %q, want absolute", profile, p)
			}
			if !strings.HasSuffix(p, profile+".sock") {
				t.Errorf("PathFor(%q) = %q, does not end in %q.sock", profile, p, profile)
			}
			if !strings.Contains(p, "wa") {
				t.Errorf("PathFor(%q) = %q, does not contain 'wa'", profile, p)
			}
		})
	}
}

// TestPathFor_DifferentProfilesProduceDifferentPaths ensures the per-
// profile flat layout actually separates daemons.
func TestPathFor_DifferentProfilesProduceDifferentPaths(t *testing.T) {
	a, err := PathFor("work")
	if err != nil {
		t.Fatalf("PathFor(work): %v", err)
	}
	b, err := PathFor("personal")
	if err != nil {
		t.Fatalf("PathFor(personal): %v", err)
	}
	if a == b {
		t.Errorf("PathFor(work) == PathFor(personal) == %q, want distinct paths", a)
	}
}

// TestPathFor_WithinSunPathBudget spot-checks that PathFor's result fits
// the platform sun_path limit for a reasonable profile name. This is
// not a regression test for FR-004 (that requires a very long profile
// name to trigger); it's a smoke test that the typical case works.
func TestPathFor_WithinSunPathBudget(t *testing.T) {
	p, err := PathFor("work-account")
	if err != nil {
		t.Fatalf("PathFor: %v", err)
	}
	// +1 for the implied NUL terminator.
	if len(p)+1 > maxSunPath {
		t.Errorf("PathFor(work-account) = %q (%d bytes), exceeds %d",
			p, len(p)+1, maxSunPath)
	}
}

// TestPath_LegacyStillWorks asserts the single-profile Path() helper
// (feature 004) continues to return a result for backward compatibility.
func TestPath_LegacyStillWorks(t *testing.T) {
	p, err := Path()
	if err != nil {
		t.Fatalf("Path(): %v", err)
	}
	if !strings.HasSuffix(p, "wa.sock") {
		t.Errorf("Path() = %q, does not end in wa.sock (legacy invariant)", p)
	}
}
