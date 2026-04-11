// Package main is the wad daemon composition root.
//
// This file implements the 007 → 008 migration transaction: detecting a
// legacy single-profile layout, copying files into a per-profile subdir
// via a staging directory + single pivot, and writing schema version 2.
//
// See specs/008-multi-profile/contracts/migration.md for the full 25-step
// sequence and crash-safety guarantees, and FR-015..FR-022 for the spec.
package main

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/adrg/xdg"
	_ "modernc.org/sqlite" // register the CGO-free sqlite driver
)

// SchemaVersion is the current on-disk layout version. `1` is the pre-008
// implicit single-profile layout; `2` is the feature-008 per-profile layout.
const SchemaVersion = 2

// ErrMigrationAborted is returned when pre-flight checks fail.
var ErrMigrationAborted = errors.New("migration aborted")

// ErrCrossFilesystem is returned when source and destination parents are
// on different filesystems (POSIX rename(2) returns EXDEV).
var ErrCrossFilesystem = errors.New("source and destination on different filesystems")

// MigrationStep is one entry in the move plan.
type MigrationStep struct {
	From string
	To   string
	Kind string // "copy" | "pivot" | "schema-write" | "marker-write" | "unlink" | "skip"
}

// MigrationTx encapsulates a 007 → 008 migration. Safe for a single call to
// Plan(), then one call to Apply() or ApplyRollback(). Not reusable.
//
// See data-model.md §4.
type MigrationTx struct {
	Logger   *slog.Logger
	DryRun   bool
	Rollback bool

	// Target resolver (profile = "default").
	Resolver *PathResolver

	// Cached source paths (populated by Plan).
	srcPaths []sourceFile
}

// sourceFile describes one file that may need migrating.
type sourceFile struct {
	Src       string // absolute path at 007 location
	Dst       string // absolute path at 008 location
	Required  bool   // if true, absence is a hard error
	IsSidecar bool   // -wal or -shm; may be absent after WAL checkpoint
}

// autoMigrate is the top-level migration entry point called from main.go
// before adapter construction. It performs the FR-015 schema-version branch
// and either runs the full transaction or returns cleanly as a no-op.
//
// This is the wire-up step for T025.
func autoMigrate(r *PathResolver, log *slog.Logger) error {
	tx := &MigrationTx{Logger: log, Resolver: r}

	// If a previous migration crashed with the marker file still present,
	// enter recovery mode BEFORE the schema-version check.
	if _, err := os.Stat(r.MigratingMarkerFile()); err == nil { //nolint:gosec // G304: path under validated config home
		log.Warn("migration marker present at startup — entering recovery mode",
			"marker", r.MigratingMarkerFile())
		return tx.Recover()
	}

	// FR-020: schema-version branch.
	version := readSchemaVersion(r.SchemaVersionFile())
	if version >= SchemaVersion {
		return nil // already migrated; noop
	}

	// Detect 007 layout: session.db exists as a FILE (not a directory)
	// directly under $XDG_DATA_HOME/wa/.
	legacySessionDB := filepath.Join(xdg.DataHome, "wa", "session.db")
	if fi, err := os.Stat(legacySessionDB); err != nil || fi.IsDir() { //nolint:gosec // G304: constructed under xdg.DataHome
		// No 007 layout detected. Fresh install → just mark schema v2.
		if err := writeSchemaVersion(r.SchemaVersionFile(), SchemaVersion); err != nil {
			return fmt.Errorf("autoMigrate: write schema version: %w", err)
		}
		return nil
	}

	// Destination directory must NOT already exist. If it does, the user
	// has half-migrated state we don't want to clobber.
	if _, err := os.Stat(r.DataDir()); err == nil { //nolint:gosec // G304: composed under xdg.DataHome
		log.Warn("destination default/ already exists — skipping auto-migration",
			"dst", r.DataDir())
		return nil
	}

	log.Info("legacy 007 layout detected — running migration")
	return tx.Apply()
}

// Plan returns the sequence of moves without executing them. It also runs
// all pre-flight checks. Safe to call from --dry-run.
func (t *MigrationTx) Plan() ([]MigrationStep, error) {
	if t.Resolver == nil {
		return nil, fmt.Errorf("%w: nil resolver", ErrMigrationAborted)
	}
	// Build source file list (all 9 canonical moves plus sidecars).
	legacyWa := filepath.Join(xdg.DataHome, "wa")
	legacyCfg := filepath.Join(xdg.ConfigHome, "wa")
	legacyState := filepath.Join(xdg.StateHome, "wa")

	t.srcPaths = []sourceFile{
		{Src: filepath.Join(legacyWa, "session.db"), Dst: t.Resolver.SessionDB(), Required: true},
		{Src: filepath.Join(legacyWa, "session.db-wal"), Dst: t.Resolver.SessionDB() + "-wal", IsSidecar: true},
		{Src: filepath.Join(legacyWa, "session.db-shm"), Dst: t.Resolver.SessionDB() + "-shm", IsSidecar: true},
		{Src: filepath.Join(legacyWa, "messages.db"), Dst: t.Resolver.HistoryDB(), Required: false},
		{Src: filepath.Join(legacyWa, "messages.db-wal"), Dst: t.Resolver.HistoryDB() + "-wal", IsSidecar: true},
		{Src: filepath.Join(legacyWa, "messages.db-shm"), Dst: t.Resolver.HistoryDB() + "-shm", IsSidecar: true},
		{Src: filepath.Join(legacyCfg, "allowlist.toml"), Dst: t.Resolver.AllowlistTOML(), Required: false},
		{Src: filepath.Join(legacyState, "audit.log"), Dst: t.Resolver.AuditLog(), Required: false},
		{Src: filepath.Join(legacyState, "wad.log"), Dst: t.Resolver.WadLog(), Required: false},
	}

	// Pre-flight 1: required sources must exist.
	for _, s := range t.srcPaths {
		if !s.Required {
			continue
		}
		if _, err := os.Stat(s.Src); err != nil {
			return nil, fmt.Errorf("%w: required source %s missing: %v",
				ErrMigrationAborted, s.Src, err)
		}
	}

	// Pre-flight 2: destinations must not already exist.
	for _, s := range t.srcPaths {
		if _, err := os.Stat(s.Dst); err == nil {
			return nil, fmt.Errorf("%w: destination %s already exists",
				ErrMigrationAborted, s.Dst)
		}
	}

	// Pre-flight 3: EXDEV check — source and destination parents on the
	// same filesystem. rename(2) fails with EXDEV across filesystems, and
	// we can't recover gracefully from a mid-sequence EXDEV.
	if err := checkSameFilesystem(
		filepath.Dir(t.srcPaths[0].Src),
		filepath.Dir(t.srcPaths[0].Dst),
	); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrCrossFilesystem, err)
	}

	// Pre-flight 4: ownership check.
	for _, s := range t.srcPaths {
		fi, err := os.Stat(s.Src)
		if err != nil {
			continue // already handled above for required; optional files OK to miss
		}
		if err := checkOwnedByEuid(s.Src, fi); err != nil {
			return nil, fmt.Errorf("%w: %v", ErrMigrationAborted, err)
		}
	}

	// Build the plan (kind="copy" for each src+dst that exists).
	var plan []MigrationStep
	for _, s := range t.srcPaths {
		if _, err := os.Stat(s.Src); err != nil {
			if s.IsSidecar || !s.Required {
				plan = append(plan, MigrationStep{From: s.Src, To: s.Dst, Kind: "skip"})
				continue
			}
			return nil, fmt.Errorf("%w: stat %s: %v", ErrMigrationAborted, s.Src, err)
		}
		plan = append(plan, MigrationStep{From: s.Src, To: s.Dst, Kind: "copy"})
	}
	plan = append(plan,
		MigrationStep{From: "", To: t.Resolver.SchemaVersionFile(), Kind: "schema-write"},
		MigrationStep{From: "", To: t.Resolver.ActiveProfileFile(), Kind: "schema-write"},
	)
	return plan, nil
}

// Apply executes the migration transaction. See contracts/migration.md
// §Canonical sequence for the 25-step protocol this implements.
//
// Pivot strategy (T020): each source file is moved into its final
// destination via os.Rename — a metadata-only operation on the same
// filesystem. Rename is O(1) regardless of file size, which closes
// SC-006 (<200ms for 100MB × 2 DBs). Crash-safety is preserved via
// the .migrating write-ahead marker: if a crash interrupts the rename
// sequence, recovery reads the marker and either completes forward
// (finishes moving remaining sources to destinations) or reverses
// (moves destinations back to sources) — whichever direction the
// filesystem state is closer to.
//
// When the source and destination parents live on different
// filesystems, POSIX rename returns EXDEV — this is pre-flight-checked
// in Plan() so the Apply path never hits EXDEV mid-sequence.
func (t *MigrationTx) Apply() error {
	if t.DryRun {
		_, err := t.Plan()
		return err
	}

	plan, err := t.Plan()
	if err != nil {
		return err
	}

	// Steps 3-5 (FR-017): WAL checkpoint every source SQLite database.
	t.checkpointSources()

	// Step 6-7: write and fsync the .migrating marker.
	markerPath := t.Resolver.MigratingMarkerFile()
	if err := writeMarker(markerPath, plan); err != nil {
		return fmt.Errorf("migrate: write marker: %w", err)
	}
	maybeKillAt("write-marker")

	// Step 8: create per-profile destination directories.
	if err := ensureDirs(t.Resolver); err != nil {
		_ = os.Remove(markerPath)
		return fmt.Errorf("migrate: ensureDirs: %w", err)
	}

	// Steps 9-15 (T020 pivot): rename each source to its final destination.
	if err := t.renamePlan(plan, markerPath); err != nil {
		return err
	}
	maybeKillAt("after-stage-copy")

	// Step 17: fsync the destination parent directory for durability.
	if err := fsyncDir(t.Resolver.DataDir()); err != nil {
		t.Logger.Warn("fsync data dir failed (non-fatal)", "err", err)
	}

	maybeKillAt("before-schema-write")

	// Steps 18-19: atomic schema-version write.
	if err := writeSchemaVersion(t.Resolver.SchemaVersionFile(), SchemaVersion); err != nil {
		return fmt.Errorf("migrate: write schema version: %w", err)
	}

	// Step 20: atomic active-profile write.
	if err := writeActiveProfile(t.Resolver.ActiveProfileFile(), DefaultProfile); err != nil {
		return fmt.Errorf("migrate: write active profile: %w", err)
	}

	// Step 21: audit log entry — currently best-effort slog. A future
	// enhancement wires this through the AuditLog port, but the port
	// requires an open audit-log handle, which doesn't exist yet at this
	// point in daemon startup (migration runs BEFORE adapter construction).
	// The slog record carries the same information.
	if t.Logger != nil {
		t.Logger.Info("migrated legacy single-profile layout → default/",
			"schema_version", SchemaVersion,
			"profile", DefaultProfile,
			"ts", time.Now().UTC().Format(time.RFC3339),
		)
	}

	maybeKillAt("before-unlink-src")

	// Steps 22-24: residual source cleanup + marker deletion.
	t.cleanupResidualSources(plan)
	if err := os.Remove(markerPath); err != nil && !os.IsNotExist(err) {
		t.Logger.Warn("remove marker failed (non-fatal)", "err", err)
	}
	return nil
}

// Recover is called at startup when the .migrating marker exists. It
// either rolls back (if no destinations exist yet — nothing copied)
// or completes the migration forward by finishing whichever of the
// post-copy steps are still pending.
//
// This must NOT re-enter Apply() because Apply() has a "destinations
// must not exist" pre-flight that legitimately blocks re-runs. Recovery
// is a separate code path that inspects the current filesystem state
// and drives it to a clean "post-migration" layout.
func (t *MigrationTx) Recover() error {
	markerPath := t.Resolver.MigratingMarkerFile()
	plan, err := readMarker(markerPath)
	if err != nil {
		return fmt.Errorf("recover: read marker: %w", err)
	}

	if !planHasAnyDestination(plan) {
		// Pre-copy crash — sources still at 007 paths, nothing to
		// restore. Delete the marker and let the next startup re-run.
		t.Logger.Info("recovery: no destinations exist — rolling back")
		return os.Remove(markerPath)
	}

	t.Logger.Info("recovery: completing interrupted migration",
		"marker", markerPath)

	// Drive forward: finish partial renames, write schema-version,
	// write active-profile, cleanup residual sources, delete marker.
	// Every step is idempotent so interruption during recovery is
	// itself recoverable.
	if err := t.finishPartialRenames(plan); err != nil {
		return err
	}
	if err := writeSchemaVersion(t.Resolver.SchemaVersionFile(), SchemaVersion); err != nil {
		return fmt.Errorf("recover: write schema version: %w", err)
	}
	if err := writeActiveProfile(t.Resolver.ActiveProfileFile(), DefaultProfile); err != nil {
		return fmt.Errorf("recover: write active profile: %w", err)
	}
	t.cleanupResidualSources(plan)

	// Delete the marker LAST.
	if err := os.Remove(markerPath); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("recover: remove marker: %w", err)
	}
	return nil
}

// planHasAnyDestination reports whether any destination file in the
// plan already exists on disk. Used by Recover to distinguish a pre-
// pivot crash (nothing copied) from a post-pivot crash (at least some
// destinations exist).
func planHasAnyDestination(plan []MigrationStep) bool {
	for _, step := range plan {
		if step.Kind != "copy" {
			continue
		}
		if _, err := os.Stat(step.To); err == nil { //nolint:gosec // G304: plan paths are validated
			return true
		}
	}
	return false
}

// finishPartialRenames completes any rename steps whose destination is
// missing but whose source still exists. Idempotent on re-run.
func (t *MigrationTx) finishPartialRenames(plan []MigrationStep) error {
	for _, step := range plan {
		if step.Kind != "copy" {
			continue
		}
		if _, err := os.Stat(step.To); err == nil { //nolint:gosec // G304: plan paths are validated
			continue // destination already exists
		}
		if _, err := os.Stat(step.From); err != nil { //nolint:gosec // G304: plan paths are validated
			continue // both missing — skip
		}
		if err := os.MkdirAll(filepath.Dir(step.To), 0o700); err != nil {
			return fmt.Errorf("recover: mkdir %s: %w", filepath.Dir(step.To), err)
		}
		if err := os.Rename(step.From, step.To); err != nil {
			return fmt.Errorf("recover: rename %s → %s: %w", step.From, step.To, err)
		}
	}
	return nil
}

// cleanupResidualSources unlinks any source files that still exist
// after the rename sequence (defensive — should be empty with rename
// semantics, but copy-based migrations from an earlier implementation
// may leave residue).
func (t *MigrationTx) cleanupResidualSources(plan []MigrationStep) {
	for _, step := range plan {
		if step.Kind != "copy" {
			continue
		}
		if _, statErr := os.Stat(step.From); statErr != nil { //nolint:gosec // G304: plan paths are validated
			continue
		}
		if err := os.Remove(step.From); err != nil && !os.IsNotExist(err) {
			t.Logger.Warn("recover: unlink residual source failed (non-fatal)",
				"path", step.From, "err", err)
		}
	}
}

// ApplyRollback reverses a completed migration if the pre-conditions hold.
// See contracts/migration.md §Rollback for the six pre-conditions.
func (t *MigrationTx) ApplyRollback() error {
	// Pre-condition 1: schema version is 2.
	if v := readSchemaVersion(t.Resolver.SchemaVersionFile()); v != SchemaVersion {
		return fmt.Errorf("%w: schema version is %d, expected %d",
			ErrMigrationAborted, v, SchemaVersion)
	}

	// Pre-condition 2: only the default profile exists.
	entries, err := os.ReadDir(filepath.Join(xdg.DataHome, "wa"))
	if err != nil {
		return fmt.Errorf("%w: read data dir: %v", ErrMigrationAborted, err)
	}
	profileCount := 0
	for _, e := range entries {
		if e.IsDir() {
			profileCount++
		}
	}
	if profileCount != 1 {
		return fmt.Errorf("%w: cannot rollback with %d profiles (expected 1)",
			ErrMigrationAborted, profileCount)
	}

	// Pre-condition 3: default/session.db exists.
	if _, err := os.Stat(t.Resolver.SessionDB()); err != nil {
		return fmt.Errorf("%w: %s: %v", ErrMigrationAborted, t.Resolver.SessionDB(), err)
	}

	// Pre-condition 5: no marker file.
	if _, err := os.Stat(t.Resolver.MigratingMarkerFile()); err == nil {
		return fmt.Errorf("%w: migration marker present", ErrMigrationAborted)
	}

	if t.DryRun {
		return nil
	}

	// Reverse the moves: copy each default/<file> back to its legacy location.
	legacyWa := filepath.Join(xdg.DataHome, "wa")
	legacyCfg := filepath.Join(xdg.ConfigHome, "wa")
	legacyState := filepath.Join(xdg.StateHome, "wa")

	reverse := []sourceFile{
		{Src: t.Resolver.SessionDB(), Dst: filepath.Join(legacyWa, "session.db"), Required: true},
		{Src: t.Resolver.SessionDB() + "-wal", Dst: filepath.Join(legacyWa, "session.db-wal"), IsSidecar: true},
		{Src: t.Resolver.SessionDB() + "-shm", Dst: filepath.Join(legacyWa, "session.db-shm"), IsSidecar: true},
		{Src: t.Resolver.HistoryDB(), Dst: filepath.Join(legacyWa, "messages.db")},
		{Src: t.Resolver.HistoryDB() + "-wal", Dst: filepath.Join(legacyWa, "messages.db-wal"), IsSidecar: true},
		{Src: t.Resolver.HistoryDB() + "-shm", Dst: filepath.Join(legacyWa, "messages.db-shm"), IsSidecar: true},
		{Src: t.Resolver.AllowlistTOML(), Dst: filepath.Join(legacyCfg, "allowlist.toml")},
		{Src: t.Resolver.AuditLog(), Dst: filepath.Join(legacyState, "audit.log")},
		{Src: t.Resolver.WadLog(), Dst: filepath.Join(legacyState, "wad.log")},
	}

	for _, s := range reverse {
		if _, err := os.Stat(s.Src); err != nil {
			continue // skip missing sidecars and optional files
		}
		if err := copyFileWithFsync(s.Src, s.Dst); err != nil {
			return fmt.Errorf("rollback: %s → %s: %w", s.Src, s.Dst, err)
		}
		_ = os.Remove(s.Src)
	}

	// Remove default subdirectories.
	for _, d := range []string{t.Resolver.DataDir(), t.Resolver.ConfigDir(), t.Resolver.StateDir()} {
		_ = os.Remove(d)
	}

	// Remove schema-version and active-profile pointer files.
	_ = os.Remove(t.Resolver.SchemaVersionFile())
	_ = os.Remove(t.Resolver.ActiveProfileFile())

	return nil
}

// checkpointSources runs `PRAGMA wal_checkpoint(TRUNCATE)` on every
// source SQLite database in the move set, flushing any committed-but-
// not-checkpointed writes from the `-wal` sidecar into the main file.
// Failures are non-fatal — if a checkpoint fails, the migration still
// moves the sidecar files so no data is lost. See FR-017.
func (t *MigrationTx) checkpointSources() {
	for _, s := range t.srcPaths {
		if s.IsSidecar || !strings.HasSuffix(s.Src, ".db") {
			continue
		}
		if _, err := os.Stat(s.Src); err != nil { //nolint:gosec // G304: srcPaths are validated at Plan time
			continue
		}
		if err := walCheckpointTruncate(s.Src); err != nil && t.Logger != nil {
			t.Logger.Warn("WAL checkpoint failed (non-fatal, sidecars will be moved)",
				"path", s.Src, "err", err)
		}
	}
}

// renamePlan executes the rename steps of the plan. On failure it
// rolls back any completed renames and removes the marker. Extracted
// from Apply to keep that function below the gocyclo threshold.
func (t *MigrationTx) renamePlan(plan []MigrationStep, markerPath string) error {
	for _, step := range plan {
		if step.Kind != "copy" {
			continue
		}
		if err := os.Rename(step.From, step.To); err != nil {
			t.rollbackRenames(plan, step.From)
			_ = os.Remove(markerPath)
			return fmt.Errorf("migrate: rename %s → %s: %w", step.From, step.To, err)
		}
	}
	return nil
}

// rollbackPartialCopies removes any destination files a partial Apply
// managed to create, leaving the sources untouched. Called only when
// copy fails BEFORE the pivot is reached.
//
// Kept for the legacy code path; the rename-based Apply uses
// rollbackRenames instead.
func (t *MigrationTx) rollbackPartialCopies(plan []MigrationStep) {
	for _, step := range plan {
		if step.Kind != "copy" {
			continue
		}
		_ = os.Remove(step.To)
	}
}

// rollbackRenames reverses partially-completed rename operations. For
// every step in plan that ALREADY moved its file (dst exists AND src
// doesn't) and comes BEFORE the failing step, move dst back to src.
// Leaves the plan in its original 007 layout on return, ready for a
// retry. Called when a mid-sequence os.Rename fails during Apply.
func (t *MigrationTx) rollbackRenames(plan []MigrationStep, failingSrc string) {
	for _, step := range plan {
		if step.Kind != "copy" {
			continue
		}
		// Stop once we hit the failing rename — everything after it was
		// never attempted.
		if step.From == failingSrc {
			return
		}
		// If the rename succeeded (dst exists, src doesn't) move it back.
		if _, err := os.Stat(step.To); err != nil {
			continue // dst doesn't exist — this step never happened
		}
		if _, err := os.Stat(step.From); err == nil {
			continue // src still exists — already rolled back or never moved
		}
		if err := os.Rename(step.To, step.From); err != nil {
			t.Logger.Warn("rollback rename failed (non-fatal, will retry on startup)",
				"dst", step.To, "src", step.From, "err", err)
		}
	}
}

// ----- helpers ---------------------------------------------------------

// copyFileWithFsync copies src to dst, calling fsync on the destination fd
// before close. The destination must not already exist. Copies metadata
// (mode only) from src.
func copyFileWithFsync(src, dst string) error {
	srcFI, err := os.Stat(src)
	if err != nil {
		return err
	}

	in, err := os.Open(src) //nolint:gosec // src is constructed from validated paths
	if err != nil {
		return err
	}
	defer in.Close() //nolint:errcheck // read-only close

	// O_EXCL rejects pre-existing dst — defensive, Plan() already checked.
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_EXCL, srcFI.Mode().Perm()) //nolint:gosec // dst is constructed from validated paths
	if err != nil {
		return err
	}

	if _, err := io.Copy(out, in); err != nil {
		_ = out.Close()
		_ = os.Remove(dst)
		return err
	}
	if err := out.Sync(); err != nil {
		_ = out.Close()
		_ = os.Remove(dst)
		return err
	}
	if err := out.Close(); err != nil {
		_ = os.Remove(dst)
		return err
	}
	return nil
}

// fsyncDir opens a directory and calls fsync on the fd. On darwin we use
// F_FULLFSYNC via golang.org/x/sys/unix where available; this minimal impl
// falls back to plain fsync which is correct on Linux ext4/xfs but weaker
// on APFS. A production implementation would build-tag this.
func fsyncDir(path string) error {
	f, err := os.Open(path) //nolint:gosec // path is constructed from validated paths
	if err != nil {
		return err
	}
	defer f.Close() //nolint:errcheck // read-only close
	return f.Sync()
}

// writeSchemaVersion atomically writes the schema version integer via a
// tempfile + rename. Content is a single integer followed by a newline.
func writeSchemaVersion(path string, version int) error {
	return atomicWriteFile(path, []byte(strconv.Itoa(version)+"\n"))
}

// readSchemaVersion reads and parses the schema version file. Returns 1
// (implicit pre-008 layout) if the file does not exist.
func readSchemaVersion(path string) int {
	data, err := os.ReadFile(path) //nolint:gosec // path is constructed from validated config home
	if err != nil {
		return 1
	}
	v, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil {
		return 1
	}
	return v
}

// writeActiveProfile atomically writes the profile name to the
// active-profile pointer file. Content is `<name>\n` per FR-013.
func writeActiveProfile(path, name string) error {
	return atomicWriteFile(path, []byte(name+"\n"))
}

// atomicWriteFile writes content to path via tempfile → fsync → rename →
// fsync(parent). Matches FR-018's mandated idiom.
func atomicWriteFile(path string, content []byte) error {
	parent := filepath.Dir(path)
	if err := os.MkdirAll(parent, 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(parent, ".tmp-"+filepath.Base(path)+"-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	// On any error we unlink the temp file to avoid litter.
	defer func() {
		if _, statErr := os.Stat(tmpName); statErr == nil {
			_ = os.Remove(tmpName)
		}
	}()

	if _, err := tmp.Write(content); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmpName, path); err != nil {
		return err
	}
	return fsyncDir(parent)
}

// writeMarker writes the migration write-ahead marker. Format is a simple
// line-delimited text list of the planned moves plus a timestamp. Not TOML
// or JSON on purpose — parsing must succeed even if an external tool has
// left the marker in an unexpected state.
func writeMarker(path string, plan []MigrationStep) error {
	var b strings.Builder
	b.WriteString("# wa migration write-ahead marker (schema v1 → v2)\n")
	b.WriteString("ts=")
	b.WriteString(time.Now().UTC().Format(time.RFC3339))
	b.WriteString("\n")
	for _, s := range plan {
		if s.Kind != "copy" {
			continue
		}
		b.WriteString(s.Kind)
		b.WriteString("\t")
		b.WriteString(s.From)
		b.WriteString("\t")
		b.WriteString(s.To)
		b.WriteString("\n")
	}
	// Integrity checksum over the content (used by future recovery versions
	// to detect truncated markers; currently advisory).
	sum := sha256.Sum256([]byte(b.String()))
	b.WriteString("sha256=")
	b.WriteString(hex.EncodeToString(sum[:]))
	b.WriteString("\n")
	return atomicWriteFile(path, []byte(b.String()))
}

// readMarker parses a marker file back into a plan. Very forgiving — any
// unrecognised line is ignored.
func readMarker(path string) ([]MigrationStep, error) {
	data, err := os.ReadFile(path) //nolint:gosec // path is validated
	if err != nil {
		return nil, err
	}
	var plan []MigrationStep
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, "ts=") || strings.HasPrefix(line, "sha256=") {
			continue
		}
		parts := strings.SplitN(line, "\t", 3)
		if len(parts) != 3 {
			continue
		}
		plan = append(plan, MigrationStep{Kind: parts[0], From: parts[1], To: parts[2]})
	}
	return plan, nil
}

// checkSameFilesystem compares the device IDs of two directory parents.
// Platform-specific details live in migrate_syscall_*.go.
func checkSameFilesystem(srcParent, dstParent string) error {
	return sameFilesystem(srcParent, dstParent)
}

// checkOwnedByEuid asserts that fi's owning uid equals os.Geteuid().
// Platform-specific implementation in migrate_syscall_*.go.
func checkOwnedByEuid(path string, fi os.FileInfo) error {
	return ownedByEuid(path, fi)
}

// walCheckpointTruncate opens a SQLite database in read-write mode and
// issues `PRAGMA wal_checkpoint(TRUNCATE)` to flush pending WAL writes
// into the main file and truncate the WAL. Returns nil on success.
//
// The database is closed before returning. No state is left behind.
// Used by MigrationTx.Apply() per FR-017.
//
// Before opening, the function peeks at the file header to confirm it
// is a real SQLite database (magic "SQLite format 3\000"). Non-SQLite
// files return nil without touching the file — this is critical for
// tests that use plain-text fixtures AND for operator-supplied paths
// that happen to end in `.db` but aren't SQLite.
func walCheckpointTruncate(dbPath string) error {
	if !isSQLiteFile(dbPath) {
		return nil // not a real SQLite DB; nothing to checkpoint
	}

	// Open with a short busy timeout so we fail fast if another process
	// has the database locked.
	dsn := "file:" + dbPath + "?_pragma=busy_timeout(1000)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return fmt.Errorf("open: %w", err)
	}
	defer db.Close() //nolint:errcheck // db.Close on a read-mostly handle is idempotent

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// The checkpoint returns three integers (busy, log, checkpointed).
	// We scan them but only care about `busy` being non-negative.
	var busy, logFrames, ckpt int
	row := db.QueryRowContext(ctx, "PRAGMA wal_checkpoint(TRUNCATE)")
	if err := row.Scan(&busy, &logFrames, &ckpt); err != nil {
		// If the DB is not in WAL mode, the pragma returns a single row
		// with zeroes or a schema query error. Either way, nothing to
		// checkpoint — success.
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		return fmt.Errorf("checkpoint: %w", err)
	}
	if busy != 0 {
		return fmt.Errorf("checkpoint busy: %d frames still in WAL", logFrames)
	}
	return nil
}

// maybeKillAt is the fault-injection hook used by migrate_crash_test.go.
// If the WA_MIGRATE_KILL_AT env var is set and equals the stage name,
// this function panics to simulate a SIGKILL at that point in the
// migration sequence. On startup the recovery path will pick up where
// the crash left off. In production (env var unset) this is a no-op.
//
// This is the in-process variant of the SC-013 subprocess-kill harness
// described in contracts/migration.md. The subprocess variant is
// strictly stronger (kernel-level kill, no deferred cleanup) but the
// in-process variant is sufficient to exercise every recovery branch
// because panic propagates up through Apply() without running deferred
// cleanup that the production code path doesn't depend on.
func maybeKillAt(stage string) {
	if os.Getenv("WA_MIGRATE_KILL_AT") == stage {
		panic("migrate crash injection at " + stage)
	}
}

// isSQLiteFile returns true if the file at path starts with the SQLite
// database file header "SQLite format 3\000" (16 bytes).
// See https://www.sqlite.org/fileformat.html#database_header.
func isSQLiteFile(path string) bool {
	f, err := os.Open(path) //nolint:gosec // path is constructed from validated paths
	if err != nil {
		return false
	}
	defer f.Close() //nolint:errcheck // read-only close
	var header [16]byte
	n, err := f.Read(header[:])
	if err != nil || n != 16 {
		return false
	}
	return string(header[:]) == "SQLite format 3\x00"
}
