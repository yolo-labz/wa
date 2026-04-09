//go:build linux

package socket

import (
	"path/filepath"

	"github.com/adrg/xdg"
)

// socketDir returns the directory that contains the daemon's unix socket on
// Linux. It uses $XDG_RUNTIME_DIR per the XDG Base Directory Specification.
func socketDir() (string, error) {
	return filepath.Join(xdg.RuntimeDir, "wa"), nil
}

// Path returns the full path to the daemon's unix domain socket on
// Linux: $XDG_RUNTIME_DIR/wa/wa.sock.
func Path() (string, error) {
	dir, err := socketDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "wa.sock"), nil
}
