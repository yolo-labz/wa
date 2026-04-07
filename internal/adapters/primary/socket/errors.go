package socket

import "errors"

// Sentinel errors for the socket adapter lifecycle and listener pre-flight.
// Each error corresponds to a distinct failure mode documented in
// contracts/socket-path.md and spec.md.
var (
	// ErrAlreadyRunning indicates another daemon instance holds the
	// single-instance lock on the sibling .lock file.
	ErrAlreadyRunning = errors.New("socket: another daemon is already running")

	// ErrInvalidPath indicates the resolved socket path is not absolute or
	// otherwise invalid for use with net.Listen("unix", ...).
	ErrInvalidPath = errors.New("socket: invalid socket path")

	// ErrPathTooLong indicates the resolved socket path exceeds the
	// platform's sun_path limit (104 bytes on darwin, 108 on linux).
	ErrPathTooLong = errors.New("socket: path exceeds sun_path limit")

	// ErrParentCreate indicates the parent directory of the socket path
	// could not be created.
	ErrParentCreate = errors.New("socket: cannot create parent directory")

	// ErrParentWorldWritable indicates the parent directory of the socket
	// path has world-writable or group-writable permissions, which is a
	// security risk.
	ErrParentWorldWritable = errors.New("socket: parent directory is world-writable or group-writable")

	// ErrParentSymlinkAttack indicates a component of the socket path's
	// parent directory is a symlink not owned by the current user, which
	// could indicate a symlink attack.
	ErrParentSymlinkAttack = errors.New("socket: symlink in parent path not owned by current user")

	// ErrListen indicates net.Listen("unix", path) failed for a reason
	// other than the path-specific checks above.
	ErrListen = errors.New("socket: listen failed")

	// ErrChmod indicates the post-creation chmod of the socket file to
	// 0600 failed.
	ErrChmod = errors.New("socket: chmod failed")

	// ErrBackpressure indicates the per-connection outbound notification
	// mailbox is full. The connection will be closed after a final error
	// frame is sent.
	ErrBackpressure = errors.New("socket: outbound mailbox full (backpressure)")

	// ErrShutdown indicates the server is shutting down and cannot accept
	// new requests or connections.
	ErrShutdown = errors.New("socket: server is shutting down")
)
