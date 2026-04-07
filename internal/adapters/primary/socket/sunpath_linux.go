//go:build linux

package socket

// maxSunPath is the maximum length of a unix domain socket path on Linux.
// From linux/un.h: #define UNIX_PATH_MAX 108.
const maxSunPath = 108
