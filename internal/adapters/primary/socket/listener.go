package socket

import (
	"fmt"
	"net"
	"os"
	"path/filepath"
)

// listen runs the pre-flight checks documented in contracts/socket-path.md
// and, if all pass, returns a unix domain socket listener at the given path.
// The caller is responsible for acquiring the single-instance lock and
// removing stale sockets before calling listen (see lock.go).
//
// Pre-flight checks in order:
//  1. Path must be absolute.
//  2. Path must not exceed the platform sun_path limit.
//  3. Parent directory must exist (created with MkdirAll 0700 if absent).
//  4. Parent directory must not be world-writable or group-writable.
//  5. Parent directory must not be a symlink owned by a different uid.
//  6. net.Listen("unix", path) creates the socket.
//  7. os.Chmod(path, 0600) tightens mode; verified via os.Stat.
//
//nolint:gocyclo // pre-flight checks are a linear sequence of guards; splitting hurts readability
func listen(path string) (net.Listener, error) {
	// Check 1: absolute path.
	if !filepath.IsAbs(path) {
		return nil, fmt.Errorf("%w: path %q is not absolute", ErrInvalidPath, path)
	}

	// Check 2: sun_path length limit (platform-specific constant).
	if len(path) > maxSunPath {
		return nil, fmt.Errorf("%w: path length %d exceeds limit %d: %s",
			ErrPathTooLong, len(path), maxSunPath, path)
	}

	parent := filepath.Dir(path)

	// Check 3: ensure parent directory exists with 0700.
	if err := os.MkdirAll(parent, 0o700); err != nil {
		return nil, fmt.Errorf("%w: %s: %v", ErrParentCreate, parent, err)
	}

	// Check 4: reject world-writable (0002) or group-writable (0020) parent.
	parentInfo, err := os.Lstat(parent)
	if err != nil {
		return nil, fmt.Errorf("%w: stat parent %s: %v", ErrParentCreate, parent, err)
	}
	parentMode := parentInfo.Mode().Perm()
	if parentMode&0o020 != 0 || parentMode&0o002 != 0 {
		return nil, fmt.Errorf("%w: %s has mode %04o; expected no group-write (0020) or world-write (0002)",
			ErrParentWorldWritable, parent, parentMode)
	}

	// Check 5: if parent is a symlink, it must be owned by the current uid.
	if parentInfo.Mode()&os.ModeSymlink != 0 {
		if err := checkSymlinkOwner(parent); err != nil {
			return nil, err
		}
	}
	// Lstat won't report ModeSymlink for a directory that is a symlink target
	// resolved via filepath.Dir — use Lstat on the raw parent to detect it.
	// Re-check with the raw parent path component.
	rawParentInfo, err := os.Lstat(parent)
	if err == nil && rawParentInfo.Mode()&os.ModeSymlink != 0 {
		if err := checkSymlinkOwner(parent); err != nil {
			return nil, err
		}
	}

	// Check 6: create the listener.
	ln, err := net.Listen("unix", path)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrListen, err)
	}

	// Check 7: tighten permissions and verify.
	if err := os.Chmod(path, 0o600); err != nil {
		_ = ln.Close()
		return nil, fmt.Errorf("%w: chmod %s: %v", ErrChmod, path, err)
	}
	fi, err := os.Stat(path)
	if err != nil {
		_ = ln.Close()
		return nil, fmt.Errorf("%w: stat %s after chmod: %v", ErrChmod, path, err)
	}
	if fi.Mode().Perm() != 0o600 {
		_ = ln.Close()
		return nil, fmt.Errorf("%w: expected mode 0600, got %04o on %s",
			ErrChmod, fi.Mode().Perm(), path)
	}

	return ln, nil
}
