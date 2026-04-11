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

// PathFor returns the per-profile socket path on Linux:
// $XDG_RUNTIME_DIR/wa/<profile>.sock.
//
// Sockets are flat (not nested inside a per-profile subdirectory) so that
// `ls $XDG_RUNTIME_DIR/wa/` enumerates running daemons at a glance. Matches
// Emacs's $XDG_RUNTIME_DIR/emacs/server and systemd's foo@instance.socket
// conventions (research.md D5).
//
// The returned path is subject to the sun_path budget check enforced by
// listener.go; callers that want to pre-flight the length should call
// CheckSunPathBudget on the result before attempting to bind.
func PathFor(profile string) (string, error) {
	dir, err := socketDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, profile+".sock"), nil
}
