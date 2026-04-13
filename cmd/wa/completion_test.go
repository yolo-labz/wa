package main

import (
	"os"
	"strings"
	"testing"
)

// TestCompleteProfileNames_EmptyDir covers the 0-profile case.
func TestCompleteProfileNames_EmptyDir(t *testing.T) {
	newXDGSandbox(t)
	names, dir := completeProfileNames(nil, nil, "")
	if len(names) != 0 {
		t.Errorf("completion on empty dir = %v, want []", names)
	}
	if dir == 0 {
		t.Log("directive =", dir) // NoFileComp = 4
	}
}

// TestCompleteProfileNames_SingleProfile exercises the 1-profile case.
func TestCompleteProfileNames_SingleProfile(t *testing.T) {
	newXDGSandbox(t)
	seedProfile(t, "work")
	names, _ := completeProfileNames(nil, nil, "")
	if len(names) != 1 || names[0] != "work" {
		t.Errorf("completion = %v, want [work]", names)
	}
}

// TestCompleteProfileNames_TwentyProfiles stress-tests the SC-008
// <50ms target at 20 profiles. No actual timing assertion here (that's
// in bench_test.go) but we do sanity-check correctness.
func TestCompleteProfileNames_TwentyProfiles(t *testing.T) {
	newXDGSandbox(t)
	// Twenty distinct profile names that all pass the regex.
	for i := range 20 {
		seedProfile(t, indexedName(i))
	}
	names, _ := completeProfileNames(nil, nil, "")
	if len(names) != 20 {
		t.Errorf("completion at 20 profiles = %d entries, want 20", len(names))
	}
}

// TestCompleteProfileNames_FiftyProfilesSC008 asserts the completion
// function correctly enumerates 50 profiles (the SC-008 stress target).
func TestCompleteProfileNames_FiftyProfilesSC008(t *testing.T) {
	newXDGSandbox(t)
	for i := range 50 {
		seedProfile(t, indexedName(i))
	}
	names, _ := completeProfileNames(nil, nil, "")
	if len(names) != 50 {
		t.Errorf("completion at 50 profiles = %d entries, want 50", len(names))
	}
}

// TestCompleteProfileNames_ExcludesIncompleteProfile confirms that a
// profile directory without session.db is NOT offered by completion.
func TestCompleteProfileNames_ExcludesIncompleteProfile(t *testing.T) {
	sandbox := newXDGSandbox(t)
	seedProfile(t, "work")
	// Create an incomplete profile (mkdir but no session.db).
	incomplete := sandbox + "/data/wa/incomplete"
	if err := os.MkdirAll(incomplete, 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	names, _ := completeProfileNames(nil, nil, "")
	for _, n := range names {
		if n == "incomplete" {
			t.Errorf("completion included incomplete profile %q", n)
		}
	}
}

// TestCompleteProfileNames_PrefixFilter spot-checks prefix matching.
func TestCompleteProfileNames_PrefixFilter(t *testing.T) {
	newXDGSandbox(t)
	seedProfile(t, "work")
	seedProfile(t, "weekend")
	seedProfile(t, "personal")
	names, _ := completeProfileNames(nil, nil, "we")
	if len(names) != 1 || names[0] != "weekend" {
		t.Errorf("prefix 'we' completion = %v, want [weekend]", names)
	}
	names, _ = completeProfileNames(nil, nil, "w")
	if len(names) != 2 {
		t.Errorf("prefix 'w' completion = %v, want 2 entries", names)
	}
	for _, n := range names {
		if !strings.HasPrefix(n, "w") {
			t.Errorf("prefix 'w' returned %q which does not start with w", n)
		}
	}
}

// indexedName returns a regex-valid profile name for index i.
// Names like "p-a0", "p-b0", ..., "p-z9" — each satisfies FR-002
// (lowercase, 2+ chars, alpha start, alphanumeric end, hyphen OK).
func indexedName(i int) string {
	return "p-" + string(rune('a'+i/10)) + string(rune('0'+i%10))
}
