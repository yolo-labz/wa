package socket

import (
	"context"
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
//  6. ListenConfig.Listen("unix", path) creates the socket (ctx-aware).
//  7. os.Chmod(path, 0600) tightens mode; verified via os.Stat.
func listen(ctx context.Context, path string) (net.Listener, error) {
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

	// Checks 3-5: parent directory validation (extracted for cognitive complexity).
	if err := validateParentDir(parent); err != nil {
		return nil, err
	}

	// Check 6: create the listener. The bind→Chmod window below is closed
	// by the parent dir, which validateParentDir enforces to mode 0700
	// (Check 5b) — no other uid can traverse into it, so the socket is
	// unreachable by anyone else regardless of its transient mode until
	// Check 7 tightens it to 0600.
	//
	// We deliberately do NOT narrow umask around the bind here. syscall.Umask
	// is process-global (not goroutine-scoped); narrowing it for this bind
	// races every other goroutine doing filesystem work in the same process
	// — e.g. a concurrent os.MkdirAll/os.Create would inherit umask 0o177 and
	// get mode 0600 (no owner-execute on a dir → EACCES on files created in
	// it). That surfaced as an order-dependent test flake under -shuffle. The
	// enforced-0700 parent + explicit Chmod give the same socket-perm
	// guarantee without the global side effect.
	ln, err := (&net.ListenConfig{}).Listen(ctx, "unix", path)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrListen, err)
	}

	// Check 7: tighten permissions and verify.
	if err := os.Chmod(path, 0o600); err != nil {
		_ = ln.Close()
		return nil, fmt.Errorf("%w: chmod %s: %w", ErrChmod, path, err)
	}
	fi, err := os.Stat(path)
	if err != nil {
		_ = ln.Close()
		return nil, fmt.Errorf("%w: stat %s after chmod: %w", ErrChmod, path, err)
	}
	if fi.Mode().Perm() != 0o600 {
		_ = ln.Close()
		return nil, fmt.Errorf("%w: expected mode 0600, got %04o on %s",
			ErrChmod, fi.Mode().Perm(), path)
	}

	return ln, nil
}

// validateParentDir runs pre-flight checks 3-5 on the socket's parent
// directory: existence, no group/world-writable, no symlink attack, and
// finally enforces a non-traversable 0700 mode. The 0700 enforcement is
// load-bearing: it is what lets listen() create the socket without
// narrowing the process-global umask (which raced concurrent goroutines).
// With the parent guaranteed private, no other uid can traverse into it,
// so the socket is unreachable by anyone else during the brief
// bind→Chmod window even though it is created with the default umask.
func validateParentDir(parent string) error {
	// Check 3: ensure parent directory exists with 0700.
	if err := os.MkdirAll(parent, 0o700); err != nil {
		return fmt.Errorf("%w: %s: %w", ErrParentCreate, parent, err)
	}

	// Check 4: reject world-writable or group-writable parent directories.
	parentInfo, err := os.Lstat(parent)
	if err != nil {
		return fmt.Errorf("%w: stat parent %s: %w", ErrParentCreate, parent, err)
	}
	parentMode := parentInfo.Mode().Perm()
	if parentMode&0o020 != 0 || parentMode&0o002 != 0 {
		return fmt.Errorf("%w: %s has mode %04o; expected no group-write (0020) or world-write (0002)",
			ErrParentWorldWritable, parent, parentMode)
	}

	// Check 5: if parent is a symlink, it must be owned by the current uid.
	if parentInfo.Mode()&os.ModeSymlink != 0 {
		if err := checkSymlinkOwner(parent); err != nil {
			return err
		}
	}
	rawParentInfo, err := os.Lstat(parent)
	if err == nil && rawParentInfo.Mode()&os.ModeSymlink != 0 {
		if err := checkSymlinkOwner(parent); err != nil {
			return err
		}
	}

	// Check 5b: enforce a non-traversable parent (mode 0700). MkdirAll only
	// sets 0700 on dirs it creates; a pre-existing looser-but-not-writable
	// parent (e.g. 0711/0755) passes Check 4 yet is traversable by other
	// uids — which would let them reach the socket during the bind→Chmod
	// window. Self-heal it to 0700 (the daemon owns its runtime dir; the
	// socket is tightened to 0600 regardless, so no working access is lost),
	// then verify no group/other bits remain. Done after the symlink-owner
	// check so we never chmod a target we do not own.
	//nolint:gosec // G302 expects ≤0600, but this is a DIRECTORY: it needs
	// owner-execute (0700) to be traversable. 0600 would make it unusable —
	// the exact EACCES failure this enforcement exists to prevent.
	if err := os.Chmod(parent, 0o700); err != nil {
		return fmt.Errorf("%w: chmod parent %s to 0700: %w", ErrParentCreate, parent, err)
	}
	healed, err := os.Stat(parent)
	if err != nil {
		return fmt.Errorf("%w: stat parent %s after chmod: %w", ErrParentCreate, parent, err)
	}
	if healed.Mode().Perm()&0o077 != 0 {
		return fmt.Errorf("%w: parent %s has mode %04o after chmod; expected 0700 (no group/other access)",
			ErrParentWorldWritable, parent, healed.Mode().Perm())
	}
	return nil
}
