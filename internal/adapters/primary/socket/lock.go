package socket

import (
	"fmt"
	"os"
	"syscall"
)

// Acquire obtains a non-blocking exclusive lock on the sibling file
// path+".lock" using flock(LOCK_EX|LOCK_NB). On success it removes any stale
// socket file at path (which is safe because the lock proves no other daemon
// is listening) and returns a release function that the caller MUST invoke at
// shutdown.
//
// If another daemon already holds the lock, Acquire returns ErrAlreadyRunning
// immediately (non-blocking).
// The .lock file is never removed — it is zero bytes and harmless; leaving it
// lets the next startup distinguish a clean exit (lock released) from a crash.
//
// Security note (feature 008 FR-044, CVE-2025-68146): the open must pass
// O_NOFOLLOW to refuse symlink traversal on the lockfile path. If an
// attacker plants a symlink at the lock path before the daemon starts, the
// open fails with ELOOP and the daemon refuses to start. This is the same
// fix as tox-dev/filelock PR #461.
//
// See research.md D8, contracts/socket-path.md §Pre-flight check 6, and
// specs/008-multi-profile/contracts/profile-paths.md §Lockfile open
// discipline.
func Acquire(path string) (release func(), err error) {
	lockPath := path + ".lock"

	// Open or create the lock file with O_NOFOLLOW (FR-044).
	f, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR|syscall.O_NOFOLLOW, 0o600) //nolint:gosec // path is validated before this call
	if err != nil {
		return nil, fmt.Errorf("%w: open lock file %s: %v", ErrAlreadyRunning, lockPath, err)
	}

	// Non-blocking exclusive lock.
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil { //nolint:gosec // fd is a valid file descriptor
		_ = f.Close()
		return nil, fmt.Errorf("%w: %v", ErrAlreadyRunning, err)
	}

	// Lock held — remove any stale socket file left by a crashed predecessor.
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		// Cannot clean up stale socket; release the lock and fail.
		_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN) //nolint:gosec // fd is a valid file descriptor
		_ = f.Close()
		return nil, fmt.Errorf("socket: remove stale socket %s: %w", path, err)
	}

	release = func() {
		_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN) //nolint:gosec // fd is a valid file descriptor
		_ = f.Close()
	}
	return release, nil
}
