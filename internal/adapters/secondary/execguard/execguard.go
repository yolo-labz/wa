// Package execguard vets resolved helper-binary paths before the daemon
// execs them (018 audit SEC-08). A binary writable by group/world — or
// owned by neither root nor the daemon's own euid — can be swapped by
// another local principal between resolution and exec, so adapters
// refuse it once at construction time (per-exec re-checks would cost
// a stat on every call for no additional safety: the swap window is
// identical either way).
package execguard

import (
	"errors"
	"fmt"
	"os"
	"syscall"
)

// ErrUntrusted is the sentinel wrapped by every Verify rejection.
var ErrUntrusted = errors.New("execguard: untrusted executable")

// Verify rejects path unless it is a regular file, not writable by
// group or world, and owned by root or the current euid. It follows
// symlinks deliberately — the check must apply to the file execve
// would actually run, not to a link in front of it.
func Verify(path string) error {
	info, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("execguard: stat %s: %w", path, err)
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("%w: %s is not a regular file", ErrUntrusted, path)
	}
	if perm := info.Mode().Perm(); perm&0o022 != 0 {
		return fmt.Errorf("%w: %s is group/world-writable (%04o)", ErrUntrusted, path, perm)
	}
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return fmt.Errorf("%w: %s: cannot determine owner", ErrUntrusted, path)
	}
	if uid := int(st.Uid); uid != 0 && uid != os.Geteuid() {
		return fmt.Errorf("%w: %s owned by uid %d, want root or uid %d", ErrUntrusted, path, uid, os.Geteuid())
	}
	return nil
}
