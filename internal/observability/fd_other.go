//go:build !unix

package observability

// openFDCount returns -1 on non-unix platforms. The operator-facing
// contract is "best effort"; a missing value in the backend dashboard
// is a better failure mode than a platform-specific stub that lies.
func openFDCount() int64 { return -1 }
