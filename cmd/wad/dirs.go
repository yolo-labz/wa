// Package main is the wad daemon composition root.
package main

import (
	"fmt"
	"os"
	"path/filepath"
)

// ensureDirs creates the per-profile XDG directories the daemon requires,
// each with mode 0700. For backward compatibility (single-profile installs
// before feature 008) it also creates the legacy top-level `wa/` directories
// so a subsequent migration has somewhere to stage from.
//
// Per FR-042, directories are created with explicit Mkdir (not MkdirAll
// alone) and then verified to have mode 0700 exactly. Callers must have
// already validated the profile via ValidateProfileName.
func ensureDirs(r *PathResolver) error {
	// Top-level XDG roots (parents of the per-profile subdir). MkdirAll is
	// fine here because these are shared across profiles.
	for _, d := range []string{
		filepath.Dir(r.DataDir()),
		filepath.Dir(r.ConfigDir()),
		filepath.Dir(r.StateDir()),
	} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			return fmt.Errorf("ensureDirs: top-level %s: %w", d, err)
		}
	}

	// Per-profile subdirectories. Explicit Mkdir + verify mode.
	for _, d := range []string{r.DataDir(), r.ConfigDir(), r.StateDir()} {
		if err := os.Mkdir(d, 0o700); err != nil && !os.IsExist(err) {
			return fmt.Errorf("ensureDirs: %s: %w", d, err)
		}
		// Tighten mode in case it was pre-existing with a wider mode.
		if err := os.Chmod(d, 0o700); err != nil {
			return fmt.Errorf("ensureDirs: chmod %s: %w", d, err)
		}
	}

	// Socket parent directory (shared across profiles, flat layout per FR-010).
	sockDir, err := r.SocketParentDir()
	if err != nil {
		return fmt.Errorf("ensureDirs: socket parent: %w", err)
	}
	if err := os.MkdirAll(sockDir, 0o700); err != nil {
		return fmt.Errorf("ensureDirs: %s: %w", sockDir, err)
	}
	if err := os.Chmod(sockDir, 0o700); err != nil {
		return fmt.Errorf("ensureDirs: chmod %s: %w", sockDir, err)
	}

	return nil
}
