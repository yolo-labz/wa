//go:build linux

package socket

import (
	"fmt"
	"net"

	"golang.org/x/sys/unix"
)

// peerUID returns the effective uid of the process at the other end of the
// unix domain socket connection. On Linux this uses SO_PEERCRED via
// getsockopt(2), accessed race-free through (*net.UnixConn).SyscallConn().
//
// See research.md D2 for the design rationale and citation trail.
func peerUID(conn *net.UnixConn) (uint32, error) {
	raw, err := conn.SyscallConn()
	if err != nil {
		return 0, fmt.Errorf("socket: SyscallConn: %w", err)
	}

	var (
		cred    *unix.Ucred
		credErr error
	)
	if err := raw.Control(func(fd uintptr) {
		cred, credErr = unix.GetsockoptUcred(int(fd), unix.SOL_SOCKET, unix.SO_PEERCRED)
	}); err != nil {
		return 0, fmt.Errorf("socket: Control: %w", err)
	}
	if credErr != nil {
		return 0, fmt.Errorf("socket: GetsockoptUcred: %w", credErr)
	}
	return cred.Uid, nil
}
