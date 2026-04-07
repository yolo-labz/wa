package socket

import "net"

// SetPeerUIDFunc replaces the package-level peerUIDFunc for testing.
// It returns the previous function so callers can restore it via t.Cleanup.
// This function exists solely for the contract test suite in sockettest/.
func SetPeerUIDFunc(fn func(*net.UnixConn) (uint32, error)) func(*net.UnixConn) (uint32, error) {
	prev := peerUIDFunc
	peerUIDFunc = fn
	return prev
}

// Listen exposes the internal listen function for contract tests that need to
// verify pre-flight error paths (ErrPathTooLong, ErrParentWorldWritable, etc.)
// without starting a full server. Production code should not call this — use
// Server.Run instead.
func Listen(path string) (net.Listener, error) {
	return listen(path)
}
