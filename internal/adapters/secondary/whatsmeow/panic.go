package whatsmeow

import (
	"context"
	"errors"
	"fmt"
	"os"

	"github.com/yolo-labz/wa/internal/domain"
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
//     audit.log, and the single-instance lockfile when configured.
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
// sidecars), AuditLog, and Lockfile when configured. ENOENT is treated
// as success; other removal errors are returned.
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
		if err := os.Remove(p); err != nil && !errors.Is(err, os.ErrNotExist) {
			errs = append(errs, fmt.Errorf("remove %s: %w", p, err))
		}
	}
	return errs
}
