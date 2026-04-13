// Package main — feature 008 benchmark harness (SC-014).
//
// These benchmarks back the SC-004 / SC-006 / SC-008 thresholds with
// reproducible numbers committed to the repo. Run via:
//
//	go test -bench=. -run=^$ ./cmd/wad/... -benchtime=10x
//
// Recorded baselines should be committed to
// specs/008-multi-profile/benchmarks.txt.
package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/adrg/xdg"
)

// BenchmarkPathResolver backs the cost of constructing a resolver and
// deriving every path. Not tied to a specific SC but informs the cost
// of the FR-042 verification path.
func BenchmarkPathResolver(b *testing.B) {
	for i := 0; i < b.N; i++ {
		r, _ := NewPathResolver("work")
		_ = r.SessionDB()
		_ = r.HistoryDB()
		_ = r.AllowlistTOML()
		_ = r.AuditLog()
	}
}

// BenchmarkMigration backs SC-006 (<200 ms for 100 MB session + history
// database pair). With the T020 rename-based pivot, migration is a
// metadata-only operation and should run in milliseconds regardless of
// file size.
//
// IMPORTANT: we must `Sync()` each fixture file before StartTimer,
// otherwise the fsync-parent-dir step in autoMigrate flushes the
// fixture's dirty pages as a side effect — which inflates the measured
// cost by 150–200 ms on APFS for a 200 MB fixture. That overhead is
// fixture setup cost, NOT migration cost, and SC-006 measures the
// latter.
func BenchmarkMigration(b *testing.B) {
	b.StopTimer()
	for i := 0; i < b.N; i++ {
		root := b.TempDir()
		b.Setenv("XDG_DATA_HOME", filepath.Join(root, "data"))
		b.Setenv("XDG_CONFIG_HOME", filepath.Join(root, "config"))
		b.Setenv("XDG_STATE_HOME", filepath.Join(root, "state"))
		b.Setenv("XDG_CACHE_HOME", filepath.Join(root, "cache"))
		b.Setenv("XDG_RUNTIME_DIR", filepath.Join(root, "run"))
		xdg.Reload()

		// Seed 100 MB session.db + 100 MB messages.db.
		legacyWa := filepath.Join(root, "data", "wa")
		_ = os.MkdirAll(legacyWa, 0o700)
		content := make([]byte, 100*1024*1024)
		writeAndSync(b, filepath.Join(legacyWa, "session.db"), content)
		writeAndSync(b, filepath.Join(legacyWa, "messages.db"), content)
		// Also sync the parent directory so the dirents are durable
		// before we start measuring.
		if f, err := os.Open(legacyWa); err == nil {
			_ = f.Sync()
			_ = f.Close()
		}
		// Drop the content reference so the GC doesn't run during the
		// timed region.
		content = nil

		r, _ := NewPathResolver("default")

		b.StartTimer()
		if err := autoMigrate(r, silentLogger()); err != nil {
			b.Fatalf("autoMigrate: %v", err)
		}
		b.StopTimer()
	}
}

// writeAndSync writes content to path and fsyncs the file before close,
// ensuring the data is durable on disk before the benchmark timer starts.
func writeAndSync(b *testing.B, path string, content []byte) {
	b.Helper()
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		b.Fatalf("open %s: %v", path, err)
	}
	if _, err := f.Write(content); err != nil {
		_ = f.Close()
		b.Fatalf("write %s: %v", path, err)
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		b.Fatalf("sync %s: %v", path, err)
	}
	if err := f.Close(); err != nil {
		b.Fatalf("close %s: %v", path, err)
	}
}

// BenchmarkCompletion backs SC-008 (<50 ms at 50 profiles). This
// benchmark runs against the daemon-side PathResolver machinery
// (enumeration lives in cmd/wa but the filesystem layout is shared;
// a 50-profile scan is purely disk I/O bounded).
func BenchmarkCompletion(b *testing.B) {
	b.StopTimer()
	root := b.TempDir()
	b.Setenv("XDG_DATA_HOME", filepath.Join(root, "data"))
	b.Setenv("XDG_CONFIG_HOME", filepath.Join(root, "config"))
	b.Setenv("XDG_STATE_HOME", filepath.Join(root, "state"))
	b.Setenv("XDG_CACHE_HOME", filepath.Join(root, "cache"))
	b.Setenv("XDG_RUNTIME_DIR", filepath.Join(root, "run"))
	xdg.Reload()

	// Seed 50 profile directories each with a session.db.
	for i := range 50 {
		name := "p-" + string(rune('a'+i/10)) + string(rune('0'+i%10))
		dir := filepath.Join(root, "data", "wa", name)
		_ = os.MkdirAll(dir, 0o700)
		_ = os.WriteFile(filepath.Join(dir, "session.db"), []byte("x"), 0o600)
	}

	b.StartTimer()
	for i := 0; i < b.N; i++ {
		matches, err := filepath.Glob(filepath.Join(root, "data", "wa", "*", "session.db"))
		if err != nil || len(matches) != 50 {
			b.Fatalf("glob: %v, %d matches", err, len(matches))
		}
	}
}
