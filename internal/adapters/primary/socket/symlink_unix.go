//go:build darwin || linux

package socket

import (
	"fmt"
	"os"
	"syscall"
)

// checkSymlinkOwner verifies that the symlink at path is owned by the
// current effective uid. If it is not, this indicates a potential symlink
// attack and returns ErrParentSymlinkAttack.
func checkSymlinkOwner(path string) error {
	fi, err := os.Lstat(path)
	if err != nil {
		return fmt.Errorf("%w: lstat %s: %v", ErrParentSymlinkAttack, path, err)
	}
	stat, ok := fi.Sys().(*syscall.Stat_t)
	if !ok {
		return fmt.Errorf("%w: cannot read owner of %s", ErrParentSymlinkAttack, path)
	}
	if stat.Uid != uint32(os.Geteuid()) { //nolint:gosec // bounded by OS uid range
		return fmt.Errorf("%w: %s is owned by uid %d, not %d",
			ErrParentSymlinkAttack, path, stat.Uid, os.Geteuid())
	}
	return nil
}
