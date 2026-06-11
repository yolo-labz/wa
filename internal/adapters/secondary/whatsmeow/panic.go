package whatsmeow

import (
	"context"
	"errors"
	"fmt"
	"os"

	"github.com/yolo-labz/wa/v2/internal/domain"
)

// PanicArtefacts names the per-profile filesystem artefacts the adapter
// must remove when Panic is invoked. Each field is an absolute path;
// the empty string disables removal of that particular artefact. Set
// once after Open via SetPanicArtefacts from the composition root.
//
// The adapter also removes the SQLite sidecar files (`-wal`, `-shm`)
// next to SessionDB and HistoryDB automatically — the caller only
// supplies the base paths.
type PanicArtefacts struct {
	SessionDB string
	HistoryDB string
	AuditLog  string
	Lockfile  string
	// MediaCacheRoot is the content-addressed media directory, removed
	// recursively (018 audit SEC-05). The cache is shared across
	// profiles; a panic on any profile wipes it whole — blobs are
	// re-downloadable, the R-07 sanitise guarantee is not.
	MediaCacheRoot string
}

// SetPanicArtefacts wires per-profile filesystem paths used by Panic.
// Safe to call any time before Panic; subsequent calls replace the
// previous configuration.
func (a *Adapter) SetPanicArtefacts(p PanicArtefacts) {
	a.panicMu.Lock()
	defer a.panicMu.Unlock()
	a.panicArtefacts = p
}

// Panic implements R-07 full-wipe semantics. It is invoked both from
// the explicit `panic` JSON-RPC method and automatically on
// `events.LoggedOut`. Steps:
//
//  1. Server-side device unlink (whatsmeow Logout) — best effort, a
//     failure here does not block the local wipe.
//  2. Clear the overlay seedSession + seedHistory maps.
//  3. Emit AuditPanic to the in-memory ring with the provided reason.
//  4. Disconnect the whatsmeow client and close the history + session
//     SQLite containers so their file handles release.
//  5. Remove session.db (+ -wal, -shm), messages.db (+ -wal, -shm),
//     audit.log, the single-instance lockfile, and the media cache
//     tree when configured.
//
// Panic is idempotent: the second call is a no-op returning nil.
// Errors from individual steps are joined; an ENOENT on removal is
// treated as success.
func (a *Adapter) Panic(ctx context.Context, reason string) error {
	a.panicMu.Lock()
	if a.panicDone {
		a.panicMu.Unlock()
		return nil
	}
	a.panicDone = true
	artefacts := a.panicArtefacts
	a.panicMu.Unlock()

	var errs []error

	// Step 1: server-side unlink, best effort.
	if a.client != nil && !a.closed.Load() {
		if err := a.client.Logout(ctx); err != nil {
			errs = append(errs, fmt.Errorf("logout: %w", err))
		}
	}

	// Step 2: overlay wipe.
	_ = a.clearSessionLocked()
	a.overlayMu.Lock()
	a.seedHistory = make(map[domain.JID][]domain.Message)
	a.overlayMu.Unlock()

	// Step 3: audit row recorded BEFORE container close so auditBuf
	// (in-memory ring) captures the reason regardless of DB state.
	a.recordAuditDetail(domain.AuditPanic, domain.JID{}, "wiped", reason)

	// Step 4: release SQLite handles.
	errs = append(errs, a.releaseSQLiteHandles()...)

	// Step 5: remove filesystem artefacts.
	errs = append(errs, removePanicArtefacts(artefacts)...)

	return errors.Join(errs...)
}

// releaseSQLiteHandles disconnects the whatsmeow client and closes
// the history + session SQLite containers. Nil handles are skipped.
func (a *Adapter) releaseSQLiteHandles() []error {
	var errs []error
	if a.client != nil {
		a.client.Disconnect()
	}
	if a.history != nil {
		if err := a.history.Close(); err != nil {
			errs = append(errs, fmt.Errorf("history close: %w", err))
		}
		a.history = nil
	}
	if a.session != nil {
		if err := a.session.Close(); err != nil {
			errs = append(errs, fmt.Errorf("session close: %w", err))
		}
		a.session = nil
	}
	return errs
}

// removePanicArtefacts deletes SessionDB, HistoryDB (+ sqlite -wal/-shm
// sidecars), AuditLog, Lockfile, and the MediaCacheRoot tree when
// configured. ENOENT is treated as success; other removal errors are
// returned. Every removal refuses to follow symlinks (018 audit
// SEC-05): a symlink at an artefact path means the data the wipe was
// meant to destroy lives elsewhere, which must surface as an error,
// and following it would hand a planted link an arbitrary-delete
// primitive.
func removePanicArtefacts(artefacts PanicArtefacts) []error {
	paths := make([]string, 0, 8)
	if artefacts.SessionDB != "" {
		paths = append(paths, artefacts.SessionDB, artefacts.SessionDB+"-wal", artefacts.SessionDB+"-shm")
	}
	if artefacts.HistoryDB != "" {
		paths = append(paths, artefacts.HistoryDB, artefacts.HistoryDB+"-wal", artefacts.HistoryDB+"-shm")
	}
	if artefacts.AuditLog != "" {
		paths = append(paths, artefacts.AuditLog)
	}
	if artefacts.Lockfile != "" {
		paths = append(paths, artefacts.Lockfile)
	}
	var errs []error
	for _, p := range paths {
		if err := removeNoFollow(p); err != nil {
			errs = append(errs, err)
		}
	}
	if artefacts.MediaCacheRoot != "" {
		if err := removeTreeNoFollow(artefacts.MediaCacheRoot); err != nil {
			errs = append(errs, err)
		}
	}
	return errs
}

// removeNoFollow unlinks p without ever following a symlink. ENOENT is
// success. A symlink at p is unlinked (the alias itself, never the
// target) but still reported as an error: the file R-07 promised to
// destroy was not destroyed.
func removeNoFollow(p string) error {
	fi, err := os.Lstat(p)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("lstat %s: %w", p, err)
	}
	if fi.Mode()&os.ModeSymlink != 0 {
		_ = os.Remove(p)
		return fmt.Errorf("wipe %s: path was a symlink; pointed-at data not destroyed", p)
	}
	if err := os.Remove(p); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("remove %s: %w", p, err)
	}
	return nil
}

// removeTreeNoFollow deletes the media-cache tree. os.RemoveAll already
// refuses to traverse symlinks inside the tree; the extra Lstat refuses
// a symlinked ROOT, so a planted link can never aim the recursive
// delete at a foreign directory.
func removeTreeNoFollow(root string) error {
	fi, err := os.Lstat(root)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("lstat %s: %w", root, err)
	}
	if fi.Mode()&os.ModeSymlink != 0 {
		_ = os.Remove(root)
		return fmt.Errorf("wipe %s: media cache root was a symlink; tree not destroyed", root)
	}
	if err := os.RemoveAll(root); err != nil {
		return fmt.Errorf("remove media cache %s: %w", root, err)
	}
	return nil
}
