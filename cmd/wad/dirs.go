// Package main is the wad daemon composition root.
package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/adrg/xdg"

	"github.com/yolo-labz/wa/internal/adapters/primary/socket"
)

// ensureDirs creates the four XDG directories the daemon requires,
// each with mode 0700.
func ensureDirs() error {
	dirs := []string{
		filepath.Join(xdg.DataHome, "wa"),
		filepath.Join(xdg.ConfigHome, "wa"),
		filepath.Join(xdg.StateHome, "wa"),
	}

	// Socket parent dir is platform-specific; use socket.Path() to derive it.
	sockPath, err := socket.Path()
	if err != nil {
		return fmt.Errorf("ensureDirs: socket path: %w", err)
	}
	dirs = append(dirs, filepath.Dir(sockPath))

	for _, d := range dirs {
		if err := os.MkdirAll(d, 0o700); err != nil {
			return fmt.Errorf("ensureDirs: %s: %w", d, err)
		}
	}
	return nil
}
