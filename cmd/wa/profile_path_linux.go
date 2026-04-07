//go:build linux

package main

import (
	"path/filepath"

	"github.com/adrg/xdg"
)

// profileSocketPath mirrors internal/adapters/primary/socket/path_linux.go
// PathFor(profile): $XDG_RUNTIME_DIR/wa/<profile>.sock.
func profileSocketPath(profile string) string {
	return filepath.Join(xdg.RuntimeDir, "wa", profile+".sock")
}
