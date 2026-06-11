// Package main — SF-04 tests.
//
// A failed renamePlan must leave the migration marker in place whenever
// rollback could not restore every completed move. Removing it anyway
// meant the next boot saw no marker and no legacy session.db, stamped
// schema v2, and hid a half-migrated tree forever.
package main

import (
	"os"
	"path/filepath"
	"testing"
)

// sf04Env builds the minimal two-step rename world: step A has already
// moved (dst exists, src gone), step B is the failing rename. Returns
// the plan plus the dirs and a pre-created marker file.
func sf04Env(t *testing.T) (plan []MigrationStep, srcDir, dstDir, marker string) {
	t.Helper()
	root := t.TempDir()
	srcDir = filepath.Join(root, "src")
	dstDir = filepath.Join(root, "dst")
	for _, d := range []string{srcDir, dstDir} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			t.Fatalf("mkdir %s: %v", d, err)
		}
	}
	if err := os.WriteFile(filepath.Join(dstDir, "a"), []byte("x"), 0o600); err != nil {
		t.Fatalf("seed dst/a: %v", err)
	}
	marker = filepath.Join(root, ".migrating")
	if err := os.WriteFile(marker, []byte("{}"), 0o600); err != nil {
		t.Fatalf("seed marker: %v", err)
	}
	plan = []MigrationStep{
		{Kind: "copy", From: filepath.Join(srcDir, "a"), To: filepath.Join(dstDir, "a")},
		{Kind: "copy", From: filepath.Join(srcDir, "b"), To: filepath.Join(dstDir, "b")},
	}
	return plan, srcDir, dstDir, marker
}

// TestAbortRenames_CompleteRollbackRemovesMarker — every completed move
// restored → marker removed, tree back at 007 for a clean retry.
func TestAbortRenames_CompleteRollbackRemovesMarker(t *testing.T) {
	plan, srcDir, _, marker := sf04Env(t)
	tx := &MigrationTx{Logger: silentLogger()}

	tx.abortRenames(plan, plan[1].From, marker)

	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Errorf("marker should be gone after complete rollback, stat err = %v", err)
	}
	if _, err := os.Stat(filepath.Join(srcDir, "a")); err != nil {
		t.Errorf("step A not restored to src: %v", err)
	}
}

// TestAbortRenames_IncompleteRollbackKeepsMarker — the SF-04 case: a
// restore fails (EACCES on the source dir) → marker must survive so
// Recover() drives the migration forward on the next boot instead of
// the fresh-install branch hiding the half-migrated tree.
func TestAbortRenames_IncompleteRollbackKeepsMarker(t *testing.T) {
	if os.Getuid() == 0 {
		t.Skip("root bypasses permission checks")
	}
	plan, srcDir, _, marker := sf04Env(t)
	if err := os.Chmod(srcDir, 0o000); err != nil {
		t.Fatalf("chmod: %v", err)
	}
	t.Cleanup(func() {
		_ = os.Chmod(srcDir, 0o700) // let TempDir cleanup succeed
	})
	tx := &MigrationTx{Logger: silentLogger()}

	tx.abortRenames(plan, plan[1].From, marker)

	if _, err := os.Stat(marker); err != nil {
		t.Errorf("marker removed despite incomplete rollback: %v", err)
	}
}

// TestRollbackRenames_SymlinkSkipReportsIncomplete — the SEC-07 skip
// leaves the destination in place, which is an un-restored move and
// must count as incomplete.
func TestRollbackRenames_SymlinkSkipReportsIncomplete(t *testing.T) {
	plan, _, dstDir, _ := sf04Env(t)
	// Replace dst/a with a symlink so refuseSymlink trips.
	target := filepath.Join(dstDir, "target")
	if err := os.WriteFile(target, []byte("y"), 0o600); err != nil {
		t.Fatal(err)
	}
	dstA := filepath.Join(dstDir, "a")
	if err := os.Remove(dstA); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, dstA); err != nil {
		t.Fatal(err)
	}
	tx := &MigrationTx{Logger: silentLogger()}

	if tx.rollbackRenames(plan, plan[1].From) {
		t.Error("rollbackRenames = true, want false when a symlink dst is skipped")
	}
}
