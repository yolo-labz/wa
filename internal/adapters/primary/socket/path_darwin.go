//go:build darwin

package socket

import (
	"os"
	"path/filepath"
)

// socketDir returns the directory that contains the daemon's unix socket on
// macOS. It resolves to ~/Library/Caches/wa rather than using xdg.RuntimeDir,
// which incorrectly maps to ~/Library/Application Support on darwin (see
// research.md §Contradicts blueprint and adrg/xdg issue #120).
//
// ~/Library/Caches/ is the Apple-documented location for non-critical,
// recomputable per-user state — excluded from Time Machine and iCloud by
// default — which is exactly right for a transient IPC socket.
func socketDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, "Library", "Caches", "wa"), nil
}

// Path returns the full path to the daemon's unix domain socket on
// macOS: ~/Library/Caches/wa/wa.sock.
//
// Deprecated: prefer PathFor(profile) for feature 008. Path() remains for
// single-profile backward compatibility and returns the legacy wa.sock path.
func Path() (string, error) {
	dir, err := socketDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "wa.sock"), nil
}

// PathFor returns the per-profile socket path on darwin:
// ~/Library/Caches/wa/<profile>.sock.
//
// darwin's sun_path limit is 104 bytes (unchanged in xnu since 4.4BSD).
// The effective limit for this layout is:
//
//	/Users/<user>/Library/Caches/wa/<profile>.sock\0
//
// Callers should validate len(result)+1 <= maxSunPath via listener.go's
// pre-flight check before attempting to bind.
func PathFor(profile string) (string, error) {
	dir, err := socketDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, profile+".sock"), nil
}
