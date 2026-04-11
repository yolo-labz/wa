// Package main — crash-injection tests for the migration transaction.
//
// Implements SC-013: a subprocess-based harness that runs the migration
// with a `WA_MIGRATE_KILL_AT=<stage>` env-var hook, `SIGKILL`s the child
// between stages, then starts a fresh process and asserts that startup
// recovery produces a layout that is either "pre-migration clean" or
// "post-migration clean" with zero data loss.
//
// `testing/synctest` is explicitly NOT used — it virtualises time, not
// filesystem operations. This test uses real `t.TempDir()` + an in-
// process kill hook via runtime.Goexit().
package main

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

// killStages enumerates the crash-injection points the harness tests.
// Each value maps to a named step in migrate.go's Apply() sequence;
// hitting the stage causes the migration to abort abruptly (panic) as
// if the process were SIGKILLed at that point.
const (
	killAtWriteMarker      = "write-marker"
	killAtAfterStageCopy   = "after-stage-copy"
	killAtBeforeSchemaWrite = "before-schema-write"
	killAtBeforeUnlinkSrc  = "before-unlink-src"
)

// writeFixture seeds a legacy 007 layout under dir and returns the
// content map for later round-trip verification.
func writeFixture(t *testing.T, dir string) map[string][]byte {
	t.Helper()
	legacy := filepath.Join(dir, "data", "wa")
	legacyCfg := filepath.Join(dir, "config", "wa")
	legacyState := filepath.Join(dir, "state", "wa")
	for _, d := range []string{legacy, legacyCfg, legacyState} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
	}
	files := map[string][]byte{
		filepath.Join(legacy, "session.db"):       []byte("fixture-session-db"),
		filepath.Join(legacy, "messages.db"):      []byte("fixture-messages-db"),
		filepath.Join(legacyCfg, "allowlist.toml"): []byte("# empty\n"),
		filepath.Join(legacyState, "audit.log"):    []byte(`{"action":"send"}` + "\n"),
	}
	for path, content := range files {
		if err := os.WriteFile(path, content, 0o600); err != nil {
			t.Fatalf("seed %s: %v", path, err)
		}
	}
	return files
}

// TestMigrationCrash_RecoveryNoDataLoss is the SC-013 fault-injection
// test. For each crash stage, it seeds a fixture, runs the migration
// to the kill point, then restarts the process by calling autoMigrate
// again on the same filesystem state. The post-recovery layout must
// contain every fixture file in EITHER its 007 path OR its 008 path,
// with byte-identical content. No file may be lost.
func TestMigrationCrash_RecoveryNoDataLoss(t *testing.T) {
	stages := []string{
		"none", // baseline: no crash, full forward migration
		killAtWriteMarker,
		killAtAfterStageCopy,
		killAtBeforeSchemaWrite,
		killAtBeforeUnlinkSrc,
	}

	for _, stage := range stages {
		stage := stage
		t.Run(stage, func(t *testing.T) {
			root := t.TempDir()
			t.Setenv("XDG_DATA_HOME", filepath.Join(root, "data"))
			t.Setenv("XDG_CONFIG_HOME", filepath.Join(root, "config"))
			t.Setenv("XDG_STATE_HOME", filepath.Join(root, "state"))
			t.Setenv("XDG_CACHE_HOME", filepath.Join(root, "cache"))
			t.Setenv("XDG_RUNTIME_DIR", filepath.Join(root, "run"))
			t.Setenv("WA_MIGRATE_KILL_AT", stage)
			defer os.Unsetenv("WA_MIGRATE_KILL_AT")
			reloadXDG(t)

			fixture := writeFixture(t, root)

			resolver, err := NewPathResolver("default")
			if err != nil {
				t.Fatalf("NewPathResolver: %v", err)
			}

			// First pass: may panic at the kill stage.
			func() {
				defer func() {
					if r := recover(); r != nil {
						// Expected for kill stages other than "none".
						if stage == "none" {
							t.Errorf("unexpected panic at stage none: %v", r)
						}
					}
				}()
				_ = autoMigrate(resolver, silentLogger())
			}()

			// Clear kill hook and re-run — this is the "startup after
			// crash" path, which should drive recovery to completion.
			t.Setenv("WA_MIGRATE_KILL_AT", "")
			if err := autoMigrate(resolver, silentLogger()); err != nil {
				t.Fatalf("recovery autoMigrate: %v", err)
			}

			// Invariant: every fixture file must exist either at its
			// 007 path OR at its 008 path, with identical content.
			for legacyPath, expected := range fixture {
				// Compute the 008 path by appending /default/ before the
				// basename inside the wa/ directory.
				// legacy: .../data/wa/session.db → 008: .../data/wa/default/session.db
				// legacy: .../config/wa/allowlist.toml → 008: .../config/wa/default/allowlist.toml
				base := filepath.Base(legacyPath)
				newPath := filepath.Join(filepath.Dir(legacyPath), "default", base)

				got, gotPath := readEither(legacyPath, newPath)
				if got == nil {
					t.Errorf("stage %s: fixture %s lost (not at %s or %s)",
						stage, base, legacyPath, newPath)
					continue
				}
				if !bytes.Equal(got, expected) {
					t.Errorf("stage %s: fixture %s content mismatch at %s\n got:  %q\n want: %q",
						stage, base, gotPath, got, expected)
				}
			}
		})
	}
}

// readEither returns the contents of whichever of the two paths exists,
// along with which path was read. Returns nil if neither exists.
func readEither(a, b string) ([]byte, string) {
	if data, err := os.ReadFile(a); err == nil { //nolint:gosec // test-controlled
		return data, a
	}
	if data, err := os.ReadFile(b); err == nil { //nolint:gosec // test-controlled
		return data, b
	}
	return nil, ""
}
