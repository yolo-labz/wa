package observability

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime/debug"
	"sort"
	"time"
)

// CrashRetentionFiles caps the number of crash dumps kept on disk.
// Oldest files are swept by SweepCrashes once this many accumulate.
const CrashRetentionFiles = 10

// CrashRetentionBytes caps the total disk usage of crash dumps. When
// either limit is hit oldest files are pruned first.
const CrashRetentionBytes int64 = 100 * 1024 * 1024 // 100 MiB

// CrashFileMode is the mode crash-dump files are created with.
const CrashFileMode os.FileMode = 0o600

// CrashDirMode is the mode of the per-profile crashes/ directory.
const CrashDirMode os.FileMode = 0o700

// CrashInfo is one entry returned by ListCrashes.
type CrashInfo struct {
	Path    string    `json:"path"`
	Name    string    `json:"name"`
	Size    int64     `json:"size"`
	ModTime time.Time `json:"modTime"`
}

// SetupCrashOutput creates the per-profile crashes/ directory (0700),
// sweeps retention, opens a new timestamped crash file (0600), and
// calls runtime/debug.SetCrashOutput against it. Callers MUST keep the
// returned cleanup function alive for the daemon's lifetime and defer
// it at shutdown — the file stays open so the kernel can write into
// it on SIGABRT even if our normal handlers are gone.
//
// Per FR-039 dir is 0700, files are 0600, retention is ≤10 files AND
// ≤100 MiB (whichever first).
func SetupCrashOutput(crashDir string) (cleanup func() error, err error) {
	if crashDir == "" {
		return nil, errors.New("crash: empty crashDir")
	}
	if err := os.MkdirAll(crashDir, CrashDirMode); err != nil {
		return nil, fmt.Errorf("crash: mkdir: %w", err)
	}
	// Tighten perms on pre-existing directory — MkdirAll won't
	// retighten an existing 0755 dir.
	if err := os.Chmod(crashDir, CrashDirMode); err != nil {
		return nil, fmt.Errorf("crash: chmod dir: %w", err)
	}

	if _, err := SweepCrashes(crashDir); err != nil {
		// Sweep failure is not fatal — log and continue so the daemon
		// still captures the next crash.
		fmt.Fprintf(os.Stderr, "crash: sweep failed: %v\n", err)
	}

	name := fmt.Sprintf("%s.log", time.Now().UTC().Format("2006-01-02T15-04-05Z"))
	path := filepath.Join(crashDir, name)
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, CrashFileMode) //nolint:gosec // path joined from caller-controlled per-profile dir
	if err != nil {
		return nil, fmt.Errorf("crash: open %s: %w", path, err)
	}
	if err := debug.SetCrashOutput(f, debug.CrashOptions{}); err != nil {
		_ = f.Close()
		return nil, fmt.Errorf("crash: SetCrashOutput: %w", err)
	}
	return f.Close, nil
}

// ListCrashes returns the dumps currently on disk, newest first.
func ListCrashes(crashDir string) ([]CrashInfo, error) {
	entries, err := os.ReadDir(crashDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("crash: read dir: %w", err)
	}
	out := make([]CrashInfo, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		out = append(out, CrashInfo{
			Path:    filepath.Join(crashDir, e.Name()),
			Name:    e.Name(),
			Size:    info.Size(),
			ModTime: info.ModTime(),
		})
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].ModTime.After(out[j].ModTime)
	})
	return out, nil
}

// SweepCrashes applies the retention policy (≤10 files AND ≤100 MiB,
// whichever first). Returns the number of files removed.
func SweepCrashes(crashDir string) (int, error) {
	crashes, err := ListCrashes(crashDir)
	if err != nil {
		return 0, err
	}
	// crashes is newest-first; reverse to prune oldest first.
	sort.Slice(crashes, func(i, j int) bool {
		return crashes[i].ModTime.Before(crashes[j].ModTime)
	})

	var total int64
	for _, c := range crashes {
		total += c.Size
	}

	removed := 0
	// Reserve budget for the file we are about to open (~1 KiB header
	// is fine; the tail is the crash dump itself which can be large).
	for len(crashes) > 0 && (len(crashes) >= CrashRetentionFiles || total > CrashRetentionBytes) {
		victim := crashes[0]
		if err := os.Remove(victim.Path); err != nil && !os.IsNotExist(err) {
			return removed, fmt.Errorf("crash: remove %s: %w", victim.Path, err)
		}
		total -= victim.Size
		crashes = crashes[1:]
		removed++
	}
	return removed, nil
}
