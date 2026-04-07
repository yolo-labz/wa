//go:build darwin

package socket

// maxSunPath is the maximum length of a unix domain socket path on macOS.
// From sys/un.h: struct sockaddr_un { ... char sun_path[104]; }.
// The limit includes the NUL terminator, but Go's net.Listen does not
// include a NUL in len(path), so the usable limit is 103 bytes. We use
// 104 as the check value per contracts/socket-path.md which specifies
// "len(path) ≤ 104".
const maxSunPath = 104
