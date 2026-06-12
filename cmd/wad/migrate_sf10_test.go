// Package main — SF-10 error-class tests.
//
// I/O failures during the migration pre-flight (EACCES, EIO, EISDIR)
// must abort with an error, not masquerade as the "missing file"
// default. The dangerous direction: an unreadable legacy probe read as
// "fresh install" writes schema v2 and permanently skips migrating
// real 007 data.
package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestReadSchemaVersion_ErrorClasses pins the three readSchemaVersion
// outcomes: missing → v1, garbage content → v1 (self-heal), I/O error →
// propagate.
func TestReadSchemaVersion_ErrorClasses(t *testing.T) {
	dir := t.TempDir()

	t.Run("missing file means v1", func(t *testing.T) {
		v, err := readSchemaVersion(filepath.Join(dir, "absent"))
		if err != nil || v != 1 {
			t.Fatalf("got (%d, %v), want (1, nil)", v, err)
		}
	})

	t.Run("garbage content means v1", func(t *testing.T) {
		p := filepath.Join(dir, "garbage")
		if err := os.WriteFile(p, []byte("not-a-number\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		v, err := readSchemaVersion(p)
		if err != nil || v != 1 {
			t.Fatalf("got (%d, %v), want (1, nil)", v, err)
		}
	})

	t.Run("io error propagates", func(t *testing.T) {
		// A directory at the version path makes ReadFile fail with a
		// non-NotExist error (EISDIR) — portable, no root or chmod
		// needed.
		p := filepath.Join(dir, "isdir")
		if err := os.MkdirAll(p, 0o700); err != nil {
			t.Fatal(err)
		}
		if _, err := readSchemaVersion(p); err == nil {
			t.Fatal("want error for unreadable version file, got nil")
		}
	})
}

// TestAutoMigrate_UnreadableLegacyProbeAborts seals the SF-10 data-loss
// path: EACCES on the legacy session.db probe must abort autoMigrate —
// NOT take the "fresh install" branch that stamps schema v2 and makes
// every later boot skip the migration.
func TestAutoMigrate_UnreadableLegacyProbeAborts(t *testing.T) {
	if os.Getuid() == 0 {
		t.Skip("root bypasses permission checks")
	}
	env := newMigrationTestEnv(t)

	// Remove +x from $XDG_DATA_HOME/wa so the session.db stat inside it
	// fails with EACCES. Schema/marker files live under configHome, so
	// the earlier pre-flight steps still succeed.
	if err := os.Chmod(env.legacyWa, 0o000); err != nil {
		t.Fatalf("chmod: %v", err)
	}
	t.Cleanup(func() {
		_ = os.Chmod(env.legacyWa, 0o700) // let TempDir cleanup succeed
	})

	err := autoMigrate(env.resolver, silentLogger())
	if err == nil {
		t.Fatal("want error for unreadable legacy probe, got nil")
	}
	if !strings.Contains(err.Error(), "probe legacy layout") {
		t.Errorf("error %q does not name the probe step", err)
	}

	// The bug under test: old code wrote schema v2 here. The file must
	// NOT exist after an aborted probe.
	if _, statErr := os.Stat(env.resolver.SchemaVersionFile()); statErr == nil {
		t.Error("schema version file written despite aborted probe")
	}
}

// TestAutoMigrate_UnreadableMarkerProbeAborts covers the sibling stat:
// an unreadable migration-marker probe must abort rather than silently
// skip crash recovery.
func TestAutoMigrate_UnreadableMarkerProbeAborts(t *testing.T) {
	if os.Getuid() == 0 {
		t.Skip("root bypasses permission checks")
	}
	env := newMigrationTestEnv(t)

	if err := os.Chmod(env.legacyConfig, 0o000); err != nil {
		t.Fatalf("chmod: %v", err)
	}
	t.Cleanup(func() {
		_ = os.Chmod(env.legacyConfig, 0o700)
	})

	err := autoMigrate(env.resolver, silentLogger())
	if err == nil {
		t.Fatal("want error for unreadable marker probe, got nil")
	}
	if !strings.Contains(err.Error(), "probe migration marker") {
		t.Errorf("error %q does not name the marker step", err)
	}
}
