// Package main — tests for cmd/wa profile resolution, sanitization,
// and the `wa profile` subcommand tree.
package main

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/adrg/xdg"
)

// reloadXDG bounces the adrg/xdg cache after tests override env vars.
func reloadXDG(t *testing.T) {
	t.Helper()
	xdg.Reload()
}

// newXDGSandbox sets up a temp XDG tree and returns the data root.
func newXDGSandbox(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	for _, sub := range []string{"data", "config", "state", "cache", "run"} {
		if err := os.MkdirAll(filepath.Join(root, sub), 0o700); err != nil {
			t.Fatalf("mkdir %s: %v", sub, err)
		}
	}
	t.Setenv("XDG_DATA_HOME", filepath.Join(root, "data"))
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(root, "config"))
	t.Setenv("XDG_STATE_HOME", filepath.Join(root, "state"))
	t.Setenv("XDG_CACHE_HOME", filepath.Join(root, "cache"))
	t.Setenv("XDG_RUNTIME_DIR", filepath.Join(root, "run"))
	reloadXDG(t)
	return root
}

// seedProfile creates a minimal on-disk profile with a session.db file.
func seedProfile(t *testing.T, name string) {
	t.Helper()
	dir := filepath.Join(xdg.DataHome, "wa", name)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "session.db"), []byte("x"), 0o600); err != nil {
		t.Fatalf("write session.db: %v", err)
	}
}

func TestValidateProfileName_CLI(t *testing.T) {
	t.Parallel()
	// CLI-side validator must accept the canonical default.
	if err := ValidateProfileName("default"); err != nil {
		t.Errorf("default should be valid: %v", err)
	}
	if err := ValidateProfileName("work"); err != nil {
		t.Errorf("work should be valid: %v", err)
	}
	if err := ValidateProfileName("list"); !errors.Is(err, ErrReservedProfileName) {
		t.Errorf("list should be reserved: %v", err)
	}
	if err := ValidateProfileName("Work"); !errors.Is(err, ErrInvalidProfileName) {
		t.Errorf("Work (uppercase) should be invalid: %v", err)
	}
	if err := ValidateProfileName("a--b"); !errors.Is(err, ErrInvalidProfileName) {
		t.Errorf("a--b (double hyphen) should be invalid: %v", err)
	}
}

func TestResolveProfile_FlagWins(t *testing.T) {
	newXDGSandbox(t)
	seedProfile(t, "work")
	t.Setenv("WA_PROFILE", "envvalue")
	r, err := ResolveProfile("work")
	if err != nil {
		t.Fatalf("ResolveProfile: %v", err)
	}
	if r.Name != "work" || r.Source != SourceFlag {
		t.Errorf("got {%s, %v}, want {work, flag}", r.Name, r.Source)
	}
}

func TestResolveProfile_EnvWhenNoFlag(t *testing.T) {
	newXDGSandbox(t)
	seedProfile(t, "work")
	t.Setenv("WA_PROFILE", "work")
	r, err := ResolveProfile("")
	if err != nil {
		t.Fatalf("ResolveProfile: %v", err)
	}
	if r.Name != "work" || r.Source != SourceEnv {
		t.Errorf("got {%s, %v}, want {work, env}", r.Name, r.Source)
	}
}

func TestResolveProfile_EmptyEnvFallsThrough(t *testing.T) {
	newXDGSandbox(t)
	// FR-001: empty WA_PROFILE must be treated as unset.
	t.Setenv("WA_PROFILE", "")
	r, err := ResolveProfile("")
	if err != nil {
		t.Fatalf("ResolveProfile: %v", err)
	}
	if r.Source != SourceDefault {
		t.Errorf("empty env should fall through to default, got %v", r.Source)
	}
}

func TestResolveProfile_ActiveFile(t *testing.T) {
	newXDGSandbox(t)
	seedProfile(t, "work")
	// Write active-profile file with BOM + trailing whitespace.
	activePath := filepath.Join(xdg.ConfigHome, "wa", "active-profile")
	_ = os.MkdirAll(filepath.Dir(activePath), 0o700)
	_ = os.WriteFile(activePath, []byte("\ufeffwork  \n"), 0o600)

	r, err := ResolveProfile("")
	if err != nil {
		t.Fatalf("ResolveProfile: %v", err)
	}
	if r.Name != "work" || r.Source != SourceActiveFile {
		t.Errorf("got {%s, %v}, want {work, active-profile-file}", r.Name, r.Source)
	}
}

func TestResolveProfile_SingletonAutoselect(t *testing.T) {
	newXDGSandbox(t)
	seedProfile(t, "work")
	r, err := ResolveProfile("")
	if err != nil {
		t.Fatalf("ResolveProfile: %v", err)
	}
	if r.Name != "work" || r.Source != SourceSingleton {
		t.Errorf("got {%s, %v}, want {work, singleton}", r.Name, r.Source)
	}
}

func TestResolveProfile_MultipleProfilesErrors(t *testing.T) {
	newXDGSandbox(t)
	seedProfile(t, "work")
	seedProfile(t, "personal")
	_, err := ResolveProfile("")
	if !errors.Is(err, ErrMultipleProfiles) {
		t.Errorf("expected ErrMultipleProfiles, got %v", err)
	}
	// Error message must list the available profiles.
	if err != nil && !strings.Contains(err.Error(), "work") {
		t.Errorf("error does not list profiles: %v", err)
	}
}

func TestResolveProfile_ZeroProfilesUsesDefault(t *testing.T) {
	newXDGSandbox(t)
	r, err := ResolveProfile("")
	if err != nil {
		t.Fatalf("ResolveProfile: %v", err)
	}
	if r.Name != "default" || r.Source != SourceDefault {
		t.Errorf("got {%s, %v}, want {default, default}", r.Name, r.Source)
	}
}

func TestEnumerateProfiles_SkipsIncompleteAndInvalid(t *testing.T) {
	newXDGSandbox(t)
	// Valid profile with session.db.
	seedProfile(t, "work")
	// Incomplete profile directory (no session.db) — must be skipped.
	_ = os.MkdirAll(filepath.Join(xdg.DataHome, "wa", "incomplete"), 0o700)
	// Invalid profile name (regex violation) — must be skipped.
	_ = os.MkdirAll(filepath.Join(xdg.DataHome, "wa", "BadName"), 0o700)
	_ = os.WriteFile(filepath.Join(xdg.DataHome, "wa", "BadName", "session.db"), []byte("x"), 0o600)

	names, err := enumerateProfiles()
	if err != nil {
		t.Fatalf("enumerateProfiles: %v", err)
	}
	if len(names) != 1 || names[0] != "work" {
		t.Errorf("enumerateProfiles = %v, want [work]", names)
	}
}

func TestSanitizeProfileName(t *testing.T) {
	// Valid → unchanged.
	safe, invalid := sanitizeProfileName("work")
	if invalid || safe != "work" {
		t.Errorf("sanitize(work) = (%q, %v), want (work, false)", safe, invalid)
	}

	// Control characters → hex-escaped and invalid=true.
	safe, invalid = sanitizeProfileName("work\x1b[31m")
	if !invalid {
		t.Error("sanitize(ansi) should be invalid=true")
	}
	if !strings.Contains(safe, "\\x1b") {
		t.Errorf("sanitize(ansi) = %q, want hex escape of \\x1b", safe)
	}

	// Name that fails regex (uppercase) — sanitization returns the raw
	// printable content with invalid=true.
	safe, invalid = sanitizeProfileName("Work")
	if !invalid {
		t.Error("sanitize(Work) should be invalid=true (uppercase)")
	}
	if safe != "Work" {
		t.Errorf("sanitize(Work) = %q, want Work", safe)
	}
}

func TestRunProfileList_EmptyDir(t *testing.T) {
	newXDGSandbox(t)
	var buf bytes.Buffer
	// runProfileList writes to *os.File; use a pipe.
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	done := make(chan struct{})
	go func() {
		_, _ = buf.ReadFrom(r)
		close(done)
	}()

	if err := runProfileList(w); err != nil {
		t.Fatalf("runProfileList: %v", err)
	}
	_ = w.Close()
	<-done

	out := buf.String()
	if !strings.Contains(out, "PROFILE") || !strings.Contains(out, "no profiles") {
		t.Errorf("output missing header or empty message:\n%s", out)
	}
}

func TestRunProfileList_SanitizesInvalidName(t *testing.T) {
	newXDGSandbox(t)
	seedProfile(t, "work")
	// Plant an out-of-band profile dir with a name that breaks the regex
	// AND contains an ANSI escape sequence.
	badDir := filepath.Join(xdg.DataHome, "wa", "bad\x1b[31m")
	if err := os.MkdirAll(badDir, 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	var buf bytes.Buffer
	rpipe, wpipe, _ := os.Pipe()
	done := make(chan struct{})
	go func() {
		_, _ = buf.ReadFrom(rpipe)
		close(done)
	}()

	if err := runProfileList(wpipe); err != nil {
		t.Fatalf("runProfileList: %v", err)
	}
	_ = wpipe.Close()
	<-done

	out := buf.String()
	// Valid profile present.
	if !strings.Contains(out, "work") {
		t.Errorf("work profile missing from output:\n%s", out)
	}
	// Invalid profile is hex-escaped (ANSI bytes not present raw).
	if strings.Contains(out, "\x1b[31m") {
		t.Errorf("raw ANSI sequence leaked into output:\n%s", out)
	}
	if !strings.Contains(out, "\\x1b") {
		t.Errorf("hex escape missing for invalid name:\n%s", out)
	}
	if !strings.Contains(out, "(invalid)") {
		t.Errorf("(invalid) marker missing:\n%s", out)
	}
}

func TestCheckCaseInsensitiveCollision(t *testing.T) {
	newXDGSandbox(t)
	// Pre-existing mixed-case directory.
	_ = os.MkdirAll(filepath.Join(xdg.DataHome, "wa", "Work"), 0o700)
	err := checkCaseInsensitiveCollision("work")
	if err == nil {
		t.Error("expected collision error for work vs Work, got nil")
	}
	// Same case → no collision (it would be caught by the later Mkdir).
	err = checkCaseInsensitiveCollision("Work")
	if err != nil {
		t.Errorf("same-case should not collide: %v", err)
	}
	// Different name → no collision.
	err = checkCaseInsensitiveCollision("personal")
	if err != nil {
		t.Errorf("personal vs Work should not collide: %v", err)
	}
}

func TestCompleteProfileNames(t *testing.T) {
	newXDGSandbox(t)
	seedProfile(t, "work")
	seedProfile(t, "weekend")
	seedProfile(t, "default")

	// Prefix "w" → {work, weekend}.
	names, _ := completeProfileNames(nil, nil, "w")
	if len(names) != 2 {
		t.Errorf("completion for 'w' = %v, want 2 entries", names)
	}
	// Prefix "def" → {default}.
	names, _ = completeProfileNames(nil, nil, "def")
	if len(names) != 1 || names[0] != "default" {
		t.Errorf("completion for 'def' = %v, want [default]", names)
	}
	// Empty prefix → all three.
	names, _ = completeProfileNames(nil, nil, "")
	if len(names) != 3 {
		t.Errorf("completion for empty = %v, want 3 entries", names)
	}
}

func TestProfileSocketPath_MatchesDaemonLayout(t *testing.T) {
	newXDGSandbox(t)
	// The CLI-side helper must return a path that matches the daemon's
	// layout convention. We can't import cmd/wad here, but we can assert
	// the shape.
	p := socketPathForProfile("work")
	if p == "" {
		t.Fatal("socketPathForProfile returned empty")
	}
	if !strings.HasSuffix(p, "work.sock") {
		t.Errorf("path does not end in work.sock: %s", p)
	}
	if !strings.Contains(p, "wa") {
		t.Errorf("path does not contain wa: %s", p)
	}
}
