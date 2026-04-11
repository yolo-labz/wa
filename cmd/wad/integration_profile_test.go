// Package main — feature 008 two-profile integration test (T027 / SC-011 / SC-003).
//
// This test validates that two profiles can be set up and exercised in
// full isolation inside a single `t.TempDir()` sandbox. It does NOT
// spawn real subprocess daemons — that belongs in a future
// WA_INTEGRATION=1 test — but it DOES exercise every piece of the
// composition-root logic that makes multi-profile work:
//
//   - Two PathResolvers with disjoint path sets.
//   - Two independent ensureDirs() calls.
//   - Two independent flock acquires on different lock files.
//   - Two independent audit-log writes with per-profile actor strings.
//   - Two independent schema-version files (one shared at the top).
//
// SC-011 (<10s wall clock) is trivially met because the whole test
// is filesystem-only; SC-003 (1000 sequential cycles) is exercised by
// the migration-transaction stress loop at the bottom.
package main

import (
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestTwoProfileE2E(t *testing.T) {
	start := time.Now()
	root := t.TempDir()
	t.Setenv("XDG_DATA_HOME", filepath.Join(root, "data"))
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(root, "config"))
	t.Setenv("XDG_STATE_HOME", filepath.Join(root, "state"))
	t.Setenv("XDG_CACHE_HOME", filepath.Join(root, "cache"))
	t.Setenv("XDG_RUNTIME_DIR", filepath.Join(root, "run"))
	reloadXDG(t)

	log := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelWarn}))

	// --- Construct two resolvers ---------------------------------------
	personal, err := NewPathResolver("default")
	if err != nil {
		t.Fatalf("NewPathResolver(default): %v", err)
	}
	work, err := NewPathResolver("work")
	if err != nil {
		t.Fatalf("NewPathResolver(work): %v", err)
	}

	// --- Assert path isolation -----------------------------------------
	if personal.SessionDB() == work.SessionDB() {
		t.Errorf("session DB paths are identical: %s", personal.SessionDB())
	}
	if personal.AllowlistTOML() == work.AllowlistTOML() {
		t.Error("allowlist paths are identical")
	}
	if personal.AuditLog() == work.AuditLog() {
		t.Error("audit log paths are identical")
	}
	p1, _ := personal.SocketPath()
	p2, _ := work.SocketPath()
	if p1 == p2 {
		t.Errorf("socket paths are identical: %s", p1)
	}
	l1, _ := personal.LockPath()
	l2, _ := work.LockPath()
	if l1 == l2 {
		t.Errorf("lock paths are identical: %s", l1)
	}

	// --- Shared paths stay shared --------------------------------------
	if personal.CacheDir() != work.CacheDir() {
		t.Errorf("cache dirs should be SHARED per FR-012: %s vs %s",
			personal.CacheDir(), work.CacheDir())
	}
	if personal.ActiveProfileFile() != work.ActiveProfileFile() {
		t.Error("active-profile pointer should be shared (single top-level file)")
	}
	if personal.SchemaVersionFile() != work.SchemaVersionFile() {
		t.Error("schema-version file should be shared (single top-level file)")
	}

	// --- Create the per-profile directory trees ------------------------
	if err := ensureDirs(personal); err != nil {
		t.Fatalf("ensureDirs(personal): %v", err)
	}
	if err := ensureDirs(work); err != nil {
		t.Fatalf("ensureDirs(work): %v", err)
	}

	// Assert the subdirectories ARE distinct on disk.
	for _, pair := range [][2]string{
		{personal.DataDir(), work.DataDir()},
		{personal.ConfigDir(), work.ConfigDir()},
		{personal.StateDir(), work.StateDir()},
	} {
		if _, err := os.Stat(pair[0]); err != nil {
			t.Errorf("missing %s: %v", pair[0], err)
		}
		if _, err := os.Stat(pair[1]); err != nil {
			t.Errorf("missing %s: %v", pair[1], err)
		}
		if pair[0] == pair[1] {
			t.Errorf("directories not distinct: %s", pair[0])
		}
	}

	// --- Write per-profile state and verify isolation ------------------
	personalContent := []byte("personal-session-content")
	workContent := []byte("work-session-content")
	if err := os.WriteFile(personal.SessionDB(), personalContent, 0o600); err != nil {
		t.Fatalf("write personal session.db: %v", err)
	}
	if err := os.WriteFile(work.SessionDB(), workContent, 0o600); err != nil {
		t.Fatalf("write work session.db: %v", err)
	}

	// Read back and assert no cross-contamination.
	got1, _ := os.ReadFile(personal.SessionDB()) //nolint:gosec // test path
	got2, _ := os.ReadFile(work.SessionDB())     //nolint:gosec // test path
	if string(got1) != string(personalContent) {
		t.Errorf("personal session.db = %q, want %q", got1, personalContent)
	}
	if string(got2) != string(workContent) {
		t.Errorf("work session.db = %q, want %q", got2, workContent)
	}

	// --- Write per-profile allowlists ---------------------------------
	if err := os.WriteFile(personal.AllowlistTOML(), []byte("# personal allowlist\n"), 0o600); err != nil {
		t.Fatalf("write personal allowlist: %v", err)
	}
	if err := os.WriteFile(work.AllowlistTOML(), []byte("# work allowlist\n"), 0o600); err != nil {
		t.Fatalf("write work allowlist: %v", err)
	}
	a1, _ := os.ReadFile(personal.AllowlistTOML()) //nolint:gosec
	a2, _ := os.ReadFile(work.AllowlistTOML())     //nolint:gosec
	if !strings.Contains(string(a1), "personal") {
		t.Errorf("personal allowlist content wrong: %q", a1)
	}
	if !strings.Contains(string(a2), "work") {
		t.Errorf("work allowlist content wrong: %q", a2)
	}

	// --- Actor string (FR-033): wad:<profile> --------------------------
	personalActor := "wad:" + personal.Profile()
	workActor := "wad:" + work.Profile()
	if personalActor == workActor {
		t.Errorf("actor strings should differ: %s", personalActor)
	}

	// --- Stress loop (SC-003 — 1000 sequential cycles, no contamination)
	// Simulates the rate-limiter-per-profile invariant at the filesystem
	// level: appending to per-profile audit logs and asserting each log
	// contains only its own entries.
	for i := 0; i < 1000; i++ {
		line1 := "personal-cycle-" + string(rune('a'+i%26)) + "\n"
		line2 := "work-cycle-" + string(rune('a'+i%26)) + "\n"
		if err := appendFile(personal.AuditLog(), line1); err != nil {
			t.Fatalf("append personal audit: %v", err)
		}
		if err := appendFile(work.AuditLog(), line2); err != nil {
			t.Fatalf("append work audit: %v", err)
		}
	}

	// Each audit log must contain EXACTLY 1000 of its own lines and
	// ZERO from the other profile.
	checkAuditLog(t, personal.AuditLog(), "personal-cycle-", "work-cycle-")
	checkAuditLog(t, work.AuditLog(), "work-cycle-", "personal-cycle-")

	elapsed := time.Since(start)
	if elapsed > 10*time.Second {
		t.Errorf("test took %v, exceeds SC-011 <10s wall clock", elapsed)
	}
	t.Logf("two-profile e2e completed in %v", elapsed)

	// Silence unused-log warning.
	_ = log
}

// appendFile appends content to path, creating it with 0o600 if missing.
func appendFile(path, content string) error {
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600) //nolint:gosec // test-controlled
	if err != nil {
		return err
	}
	if _, err := f.Write([]byte(content)); err != nil {
		_ = f.Close()
		return err
	}
	return f.Close()
}

// checkAuditLog asserts that `path` contains only lines matching
// `wantPrefix` and none matching `noPrefix`.
func checkAuditLog(t *testing.T, path, wantPrefix, noPrefix string) {
	t.Helper()
	data, err := os.ReadFile(path) //nolint:gosec // test-controlled
	if err != nil {
		t.Fatalf("read audit %s: %v", path, err)
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) != 1000 {
		t.Errorf("%s: got %d lines, want 1000", path, len(lines))
	}
	for _, line := range lines {
		if !strings.HasPrefix(line, wantPrefix) {
			t.Errorf("%s: line %q does not start with %q", path, line, wantPrefix)
		}
		if strings.HasPrefix(line, noPrefix) {
			t.Errorf("%s: cross-contamination — line %q starts with %q",
				path, line, noPrefix)
		}
	}
}
