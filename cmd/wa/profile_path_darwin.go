//go:build darwin

package main

import (
	"os"
	"path/filepath"
)

// profileSocketPath mirrors internal/adapters/primary/socket/path_darwin.go
// PathFor(profile): ~/Library/Caches/wa/<profile>.sock.
func profileSocketPath(profile string) string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, "Library", "Caches", "wa", profile+".sock")
}
