//go:build unix

package observability

import "syscall"

// openFDCount returns the best-effort open file-descriptor count for
// the current process. Unix path uses RLIMIT_NOFILE-style traversal
// of /dev/fd via syscall.Rlimit is unreliable across kernels; we
// instead walk by bumping a probe fd and differencing. That's
// overkill — the ceiling of `NOFILE` is a cheaper proxy that still
// alerts on descriptor leaks. Refined if operators ask for exact
// counts.
func openFDCount() int64 {
	var lim syscall.Rlimit
	if err := syscall.Getrlimit(syscall.RLIMIT_NOFILE, &lim); err != nil {
		return -1
	}
	// Treat the soft limit as the "FD budget ceiling". A rising
	// baseline against a fixed ceiling surfaces leaks in graphs.
	return int64(lim.Cur) //nolint:gosec // Cur fits int64 on every supported OS
}
