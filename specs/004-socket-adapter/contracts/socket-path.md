# Contract: Socket Path Resolver

**Feature**: 004-socket-adapter

This contract defines the filesystem path the server listens on and the pre-flight checks it performs against that path and its parent. It is a small contract but it is the source of several cross-platform bugs in real-world daemons, so it is documented explicitly.

## Platform mapping

| OS | Socket directory | Socket file |
|---|---|---|
| Linux | `$XDG_RUNTIME_DIR/wa/` | `$XDG_RUNTIME_DIR/wa/wa.sock` |
| macOS | `$HOME/Library/Caches/wa/` | `$HOME/Library/Caches/wa/wa.sock` |

Implementation is in two build-tagged files: `path_linux.go` and `path_darwin.go`. No runtime `runtime.GOOS` switch — Go's build tag system enforces platform-specific compilation, which surfaces platform bugs at build time rather than runtime.

### Linux

`$XDG_RUNTIME_DIR` is provided by `github.com/adrg/xdg`'s `xdg.RuntimeDir`. If the environment variable is unset (rare on modern distros with systemd-logind), adrg/xdg falls back to `/run/user/<uid>`. If that directory does not exist, adrg/xdg returns an error, which is propagated to the caller as a server startup failure with a clear message.

### macOS

`$HOME` is provided by `os.UserHomeDir()`. We do **not** use `adrg/xdg.RuntimeDir` on darwin because it returns `~/Library/Application Support`, which is backed up by Time Machine and iCloud — wrong semantics for a transient IPC socket. See `research.md` §Contradicts blueprint for the citation trail.

## Pre-flight checks

Before calling `net.Listen("unix", path)`, the server MUST run the following checks in order. Any failure is a fatal startup error.

### 1. Absolute path

The resolved socket path MUST be absolute. A relative path is a bug and the server returns `ErrInvalidPath`.

### 2. Length limit

On darwin, `sun_path` is 104 bytes including the NUL terminator (`sys/un.h`). On linux, it is 108 bytes. The server MUST check `len(path) ≤ 104` on darwin and `≤ 108` on linux. On overflow, the server returns a specific error whose message includes both the limit and the resolved path length.

### 3. Parent directory existence

If the parent directory does not exist, the server MUST create it with mode `0700` and owner equal to the effective uid. `os.MkdirAll(parent, 0700)` is the correct primitive.

### 4. Parent directory permissions

If the parent directory exists but is world-writable (mode bits `0002`) or group-writable (mode bits `0020`), the server MUST refuse to start and return a descriptive error. This prevents a symlink-in-parent attack where another user with write on the parent substitutes a symlink for the socket file.

### 5. Symlink in parent

If the parent directory is itself a symlink, the server MUST refuse to start **unless** the symlink target is also owned by the server's uid. This prevents a different attack where a shared-tmp directory contains a user-controlled symlink pointing to the server's intended socket directory.

### 6. Stale socket removal (gated by lock)

After the `.lock` file is flock'd exclusively (see `research.md` D8), if a file exists at the target socket path, the server MUST `os.Remove` it. The lock proves no other daemon is listening on the stale socket, so the removal is safe.

If the `.lock` cannot be flock'd, the server MUST NOT touch the socket file and MUST return `ErrAlreadyRunning` with a message pointing at the path.

### 7. Listener creation

`net.Listen("unix", path)` creates the socket. Immediately after, `os.Chmod(path, 0600)` tightens the mode in case the OS default was looser. The server verifies the mode by `os.Stat` and fails startup if the observed mode is not `0600`.

## Post-shutdown cleanup

After `wg.Wait()` returns during graceful shutdown, the server MUST:

1. `os.Remove(socketPath)` — ignore ENOENT; other errors are logged at WARN level but do not prevent shutdown
2. Release the `lockedfile.Mutex` lock by calling the unlock function returned from `Lock()`

The `.lock` sibling file is **not** removed. Leaving it in place is correct: it lets the next startup know the previous process may have exited cleanly (lock released) and is safe to unlink the stale socket file. The lock file itself is zero bytes and of no consequence.

## Error taxonomy

| Error | Cause | How the caller should handle it |
|---|---|---|
| `ErrInvalidPath` | resolved path is relative or empty | Programmer error; abort daemon startup |
| `ErrPathTooLong` | resolved path exceeds `sun_path` limit | Operator error (e.g., very long `$HOME`); abort and print remedy |
| `ErrParentCreate` | cannot create parent directory | OS/permissions error; abort and print remedy |
| `ErrParentWorldWritable` | parent dir is world- or group-writable | Security risk; abort and print `chmod 700` remedy |
| `ErrParentSymlinkAttack` | parent dir is a symlink owned by another uid | Abort; do not dereference |
| `ErrAlreadyRunning` | another daemon holds the lock | Abort; print PID if available from the lock file contents |
| `ErrListen` | `net.Listen` returned an error | Abort; print the wrapped error |
| `ErrChmod` | the observed mode after chmod is not `0600` | Abort; OS may have a umask or ACL surprise |

Each error type is a sentinel value in `errors.go` so callers can use `errors.Is` to distinguish them. All are exported from the `socket` package so `cmd/wad` can format them for the user.

## Test coverage requirement

Every numbered check in §Pre-flight checks MUST have a corresponding table-driven test in `sockettest/listener_test.go` (or in-package equivalent). The symlink-attack tests require `os.Symlink` and may be skipped on CI runners where symlinks are not writable — they MUST still compile unconditionally.
