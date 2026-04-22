// Package slogaudit implements the app.AuditLog port as a JSON-line
// append-only log file backed by slog.JSONHandler. Each Record call is
// synchronous and mutex-guarded; out-of-order writes (based on
// AuditEvent.TS) are rejected.
package slogaudit

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/yolo-labz/wa/internal/domain"
)

// ErrOutOfOrder is returned by Record when the event timestamp is not
// strictly after the last recorded event.
var ErrOutOfOrder = errors.New("slogaudit: out-of-order timestamp")

// Audit implements the app.AuditLog port. It writes JSON lines to an
// append-only file, one line per Record call.
type Audit struct {
	logger *slog.Logger
	file   *os.File
	path   string
	mu     sync.Mutex
	lastTS time.Time
}

// Open creates (or opens) the audit log at path. The parent directory
// is created with mode 0700; the file is opened O_APPEND|O_CREATE|O_WRONLY
// with mode 0600.
func Open(path string) (*Audit, error) {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("slogaudit: mkdir %s: %w", dir, err)
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600) //nolint:gosec // path from XDG state dir, validated at startup
	if err != nil {
		return nil, fmt.Errorf("slogaudit: open %s: %w", path, err)
	}
	h := slog.NewJSONHandler(f, &slog.HandlerOptions{Level: slog.LevelInfo})
	return &Audit{
		logger: slog.New(h),
		file:   f,
		path:   path,
	}, nil
}

// Path returns the audit log path the store was opened against.
func (a *Audit) Path() string { return a.path }

// Rotate atomically swaps the live audit log for a new empty file
// (FR-038). The current file is renamed to `<path>.YYYYMMDD-HHMMSS`
// (UTC), and a new file is opened at `path` with mode 0600. The
// returned rotatedPath is the old filename so callers can print or
// index it. Rotate holds the same mutex as Record, so no entries are
// lost in the swap.
func (a *Audit) Rotate() (rotatedPath string, err error) {
	a.mu.Lock()
	defer a.mu.Unlock()

	if a.file == nil {
		return "", fmt.Errorf("slogaudit: rotate: file not open")
	}

	ts := time.Now().UTC().Format("20060102-150405")
	rotated := a.path + "." + ts

	if err := a.file.Close(); err != nil {
		return "", fmt.Errorf("slogaudit: rotate: close current: %w", err)
	}
	if err := os.Rename(a.path, rotated); err != nil {
		// Reopen original so we don't lose the writer on rename failure.
		f, reopenErr := os.OpenFile(a.path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600) //nolint:gosec // path was validated by Open
		if reopenErr == nil {
			a.file = f
			a.logger = slog.New(slog.NewJSONHandler(f, &slog.HandlerOptions{Level: slog.LevelInfo}))
		}
		return "", fmt.Errorf("slogaudit: rotate: rename %s: %w", a.path, err)
	}

	f, err := os.OpenFile(a.path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600) //nolint:gosec // path was validated by Open
	if err != nil {
		return rotated, fmt.Errorf("slogaudit: rotate: open new: %w", err)
	}
	a.file = f
	a.logger = slog.New(slog.NewJSONHandler(f, &slog.HandlerOptions{Level: slog.LevelInfo}))
	a.lastTS = time.Time{}
	return rotated, nil
}

// Record writes a single audit event as a JSON line. It rejects events
// whose TS is not strictly after the last recorded event's TS.
func (a *Audit) Record(_ context.Context, e domain.AuditEvent) error {
	a.mu.Lock()
	defer a.mu.Unlock()

	if !a.lastTS.IsZero() && !e.TS.After(a.lastTS) {
		return fmt.Errorf("%w: event %s at %s <= last %s",
			ErrOutOfOrder, e.Action, e.TS.Format(time.RFC3339Nano), a.lastTS.Format(time.RFC3339Nano))
	}

	a.logger.Info("audit",
		slog.String("actor", e.Actor),
		slog.String("action", e.Action.String()),
		slog.String("subject", e.Subject.String()),
		slog.String("decision", e.Decision),
		slog.String("detail", e.Detail),
	)
	a.lastTS = e.TS
	return nil
}

// Close closes the underlying file.
func (a *Audit) Close() error {
	return a.file.Close()
}
