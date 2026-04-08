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

// SocketPath returns the full path to the daemon's unix domain socket on
// macOS: ~/Library/Caches/wa/wa.sock.
func SocketPath() (string, error) {
	dir, err := socketDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "wa.sock"), nil
}
