// Package main — runtime directory verification (FR-042).
//
// Even though the listener in internal/adapters/primary/socket/listener.go
// already performs feature-004 parent-dir checks, feature 008 tightens
// the contract further: the per-profile socket parent MUST be mode 0700
// exactly, owned by euid, and NOT a symlink. This file holds the
// composition-root-level verifier that runs BEFORE the socket adapter
// is even constructed, giving operators a clear error at startup.
package main

import (
	"errors"
	"fmt"
	"os"
)

// ErrRuntimeDirInsecure is returned when the socket parent directory
// fails one of the four FR-042 checks.
var ErrRuntimeDirInsecure = errors.New("runtime directory insecure")

// verifyRuntimeParent asserts the four FR-042 pre-bind checks on the
// socket parent directory. Any violation is a hard fail at exit code 78.
//
// Checks (in order):
//  1. Path exists and is a directory (via Lstat, so symlinks don't fool it).
//  2. Mode is exactly 0700 (no group or other bits, nothing more, nothing less).
//  3. Owning uid equals os.Geteuid().
//  4. Not a symlink.
//
// This is the composition-root verifier; the socket adapter's listener.go
// performs a similar but weaker check (rejects world/group writable). The
// two layers are defence-in-depth.
func verifyRuntimeParent(path string) error {
	fi, err := os.Lstat(path)
	if err != nil {
		return fmt.Errorf("%w: lstat %s: %v", ErrRuntimeDirInsecure, path, err)
	}

	// Check 4 (first, since other checks don't make sense on symlinks):
	// not a symlink.
	if fi.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("%w: %s is a symlink", ErrRuntimeDirInsecure, path)
	}

	// Check 1: directory.
	if !fi.IsDir() {
		return fmt.Errorf("%w: %s is not a directory", ErrRuntimeDirInsecure, path)
	}

	// Check 2: mode 0700 exactly.
	if fi.Mode().Perm() != 0o700 {
		return fmt.Errorf("%w: %s has mode %04o, required 0700",
			ErrRuntimeDirInsecure, path, fi.Mode().Perm())
	}

	// Check 3: owned by euid.
	if err := ownedByEuid(path, fi); err != nil {
		return fmt.Errorf("%w: %v", ErrRuntimeDirInsecure, err)
	}

	return nil
}
