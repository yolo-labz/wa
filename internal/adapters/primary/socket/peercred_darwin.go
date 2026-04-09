//go:build darwin

package socket

import (
	"fmt"
	"net"

	"golang.org/x/sys/unix"
)

// peerUID returns the effective uid of the process at the other end of the
// unix domain socket connection. On macOS this uses LOCAL_PEERCRED via
// getsockopt(2) returning an Xucred struct, accessed race-free through
// (*net.UnixConn).SyscallConn().
//
// See research.md D2 for the design rationale and citation trail.
func peerUID(conn *net.UnixConn) (uint32, error) {
	raw, err := conn.SyscallConn()
	if err != nil {
		return 0, fmt.Errorf("socket: SyscallConn: %w", err)
	}

	var (
		cred    *unix.Xucred
		credErr error
	)
	if err := raw.Control(func(fd uintptr) {
		cred, credErr = unix.GetsockoptXucred(int(fd), unix.SOL_LOCAL, unix.LOCAL_PEERCRED)
	}); err != nil {
		return 0, fmt.Errorf("socket: Control: %w", err)
	}
	if credErr != nil {
		return 0, fmt.Errorf("socket: GetsockoptXucred: %w", credErr)
	}
	return cred.Uid, nil
}
