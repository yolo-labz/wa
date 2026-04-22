package observability

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// TestCrashDumpWritten asserts that SetupCrashOutput opens a
// timestamped file 0600 in a 0700 dir and that the returned closer
// flushes a successful write.
func TestCrashDumpWritten(t *testing.T) {
	dir := t.TempDir()
	crashDir := filepath.Join(dir, "crashes")

	closer, err := SetupCrashOutput(crashDir)
	if err != nil {
		t.Fatalf("SetupCrashOutput: %v", err)
	}
	defer func() { _ = closer() }()

	info, err := os.Stat(crashDir)
	if err != nil {
		t.Fatalf("stat crashDir: %v", err)
	}
	if info.Mode().Perm() != 0o700 {
		t.Errorf("crashDir perm = %o, want 0700", info.Mode().Perm())
	}

	crashes, err := ListCrashes(crashDir)
	if err != nil {
		t.Fatalf("ListCrashes: %v", err)
	}
	if len(crashes) != 1 {
		t.Fatalf("expected 1 crash file, got %d", len(crashes))
	}
	finfo, err := os.Stat(crashes[0].Path)
	if err != nil {
		t.Fatalf("stat file: %v", err)
	}
	if finfo.Mode().Perm() != CrashFileMode {
		t.Errorf("file perm = %o, want %o", finfo.Mode().Perm(), CrashFileMode)
	}
}

// TestCrashRetention10AndGB asserts files beyond the 10-file retention
// are pruned oldest-first.
func TestCrashRetention10AndGB(t *testing.T) {
	dir := t.TempDir()

	// Seed 12 dummy crash files with staggered mtimes so the oldest
	// can be identified.
	for i := 0; i < 12; i++ {
		path := filepath.Join(dir, fmt.Sprintf("crash-%02d.log", i))
		if err := os.WriteFile(path, []byte("x"), CrashFileMode); err != nil {
			t.Fatalf("seed: %v", err)
		}
		mtime := time.Now().Add(-time.Duration(12-i) * time.Minute)
		if err := os.Chtimes(path, mtime, mtime); err != nil {
			t.Fatalf("chtimes: %v", err)
		}
	}

	removed, err := SweepCrashes(dir)
	if err != nil {
		t.Fatalf("SweepCrashes: %v", err)
	}
	if removed < 3 {
		t.Errorf("expected ≥3 files removed (12 → <=9 to leave room for new), got %d", removed)
	}

	// The oldest two files (00, 01) must be gone.
	for _, gone := range []string{"crash-00.log", "crash-01.log"} {
		if _, err := os.Stat(filepath.Join(dir, gone)); !os.IsNotExist(err) {
			t.Errorf("expected %s to be pruned, stat err = %v", gone, err)
		}
	}

	// The newest (11) must survive.
	if _, err := os.Stat(filepath.Join(dir, "crash-11.log")); err != nil {
		t.Errorf("newest file pruned incorrectly: %v", err)
	}
}

// TestCrashListEnumerates asserts ListCrashes returns files sorted
// newest-first and does not recurse into subdirectories.
func TestCrashListEnumerates(t *testing.T) {
	dir := t.TempDir()

	files := []struct {
		name  string
		delta time.Duration
	}{
		{"a.log", -3 * time.Hour},
		{"b.log", -1 * time.Hour},
		{"c.log", -2 * time.Hour},
	}
	for _, f := range files {
		path := filepath.Join(dir, f.name)
		if err := os.WriteFile(path, []byte("z"), CrashFileMode); err != nil {
			t.Fatalf("write %s: %v", f.name, err)
		}
		mtime := time.Now().Add(f.delta)
		if err := os.Chtimes(path, mtime, mtime); err != nil {
			t.Fatalf("chtimes: %v", err)
		}
	}
	// Subdirectory that must NOT appear.
	if err := os.MkdirAll(filepath.Join(dir, "nested"), 0o700); err != nil {
		t.Fatalf("mkdir nested: %v", err)
	}

	got, err := ListCrashes(dir)
	if err != nil {
		t.Fatalf("ListCrashes: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("len = %d, want 3", len(got))
	}
	if got[0].Name != "b.log" || got[1].Name != "c.log" || got[2].Name != "a.log" {
		t.Errorf("order = %v %v %v, want b c a (newest-first)", got[0].Name, got[1].Name, got[2].Name)
	}
}
