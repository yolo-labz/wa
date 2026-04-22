package main

import (
	"path/filepath"

	"github.com/adrg/xdg"
)

// schemaVersionPath returns the top-level schema-version file path.
// Matches cmd/wad/profile.go: SchemaVersionFile.
func schemaVersionPath() (string, error) {
	return filepath.Join(xdg.ConfigHome, "wa", ".schema-version"), nil
}

// auditLogPath returns the per-profile audit log path for the active
// profile ($XDG_STATE_HOME/wa/<profile>/audit.log).
func auditLogPath() (string, error) {
	return filepath.Join(xdg.StateHome, "wa", resolvedProfileName, "audit.log"), nil
}

// wadLogPath returns the per-profile daemon log path.
func wadLogPath() (string, error) {
	return filepath.Join(xdg.StateHome, "wa", resolvedProfileName, "wad.log"), nil
}

// backupsDirPath returns the per-profile migration backups directory.
func backupsDirPath() (string, error) {
	return filepath.Join(xdg.StateHome, "wa", resolvedProfileName, "backups"), nil
}
