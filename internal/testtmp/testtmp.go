// Package testtmp keeps unix-socket tests independent of a long $TMPDIR.
// It is imported only from _test.go files (TestMain).
package testtmp

import (
	"os"
)

// maxBase is the longest TMPDIR left alone. sockaddr_un.sun_path holds 108
// bytes on Linux (104 on macOS); the test name, its random suffix and the
// socket file name already take about 70 of them.
const maxBase = 30

// Shorten repoints TMPDIR at a short directory under /tmp when the current
// one is longer than maxBase, so t.TempDir and os.MkdirTemp stay inside the
// sun_path limit. It returns a cleanup func that restores TMPDIR and removes
// the directory. If no short directory can be made, it changes nothing.
func Shorten() (cleanup func()) {
	orig, had := os.LookupEnv("TMPDIR")
	if len(os.TempDir()) <= maxBase {
		return func() {}
	}
	dir, err := os.MkdirTemp("/tmp", "wa-t-")
	if err != nil {
		return func() {}
	}
	_ = os.Setenv("TMPDIR", dir)
	return func() {
		if had {
			_ = os.Setenv("TMPDIR", orig)
		} else {
			_ = os.Unsetenv("TMPDIR")
		}
		_ = os.RemoveAll(dir)
	}
}
