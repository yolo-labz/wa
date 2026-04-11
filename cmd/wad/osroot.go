// Package main — defense-in-depth file operations via os.Root (Go 1.24+).
//
// FR-048 mandates filepath.IsLocal as the primary defense-in-depth layer
// against path traversal. os.Root provides a second layer: traversal
// attempts that escape the root via symlinks or `..` components are
// rejected at the syscall level, not just lexically.
//
// Integration strategy: rather than rewriting every path-join site, we
// provide a single `openDataRoot()` helper that returns an os.Root
// scoped to $XDG_DATA_HOME/wa/. Callers that want symlink-traversal
// resistance (currently: the migration transaction's staging lookup
// for sidecar files) use this helper. Callers that need cross-XDG
// paths continue to use PathResolver directly — they're guarded by
// ValidateProfileName + filepath.IsLocal at construction time.
//
// The os.Root wrapper is defensive: if the root can't be opened (e.g.
// the directory doesn't exist yet during first startup), the caller
// falls back to ordinary filepath.Join — the fallback is still
// safe because FR-002/FR-048 guarantee the profile name doesn't
// contain path-traversal bytes.
//
// CVE-2026-32282 note: Go 1.24.x fixed a `Root.Chmod` symlink race via
// fchmodat2. The repo's go.mod pins `go 1.26.1`, which includes the
// fix.
package main

import (
	"os"
	"path/filepath"

	"github.com/adrg/xdg"
)

// openDataRoot returns an os.Root scoped to $XDG_DATA_HOME/wa/. The
// caller MUST Close() it when done. Returns nil + error if the
// directory doesn't exist; callers should fall back to ordinary
// filepath.Join in that case.
//
// Uses of os.Root within the returned handle are symlink-traversal
// resistant: a path like "default/../../../etc/passwd" is rejected at
// the syscall level. This is the FR-048 defense-in-depth layer.
func openDataRoot() (*os.Root, error) {
	return os.OpenRoot(filepath.Join(xdg.DataHome, "wa"))
}

// statInDataRoot reports whether a name exists within $XDG_DATA_HOME/wa/,
// using os.Root to prevent symlink traversal out of the root. Returns
// (false, nil) if the root directory doesn't exist.
//
// This is a convenience wrapper used by the migration transaction's
// profile enumeration; a future refactor can extend the Root pattern
// to other call sites.
func statInDataRoot(name string) (exists bool, err error) {
	root, err := openDataRoot()
	if err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, err
	}
	defer root.Close() //nolint:errcheck // read-only close

	if _, err := root.Stat(name); err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}
