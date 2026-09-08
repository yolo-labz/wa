//go:build unix

package main

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

// flockExclusive takes a non-blocking exclusive lock, matching what the
// history store does.
func flockExclusive(f *os.File) error {
	return unix.Flock(int(f.Fd()), unix.LOCK_EX|unix.LOCK_NB)
}

// TestUnknownArgsNeverTouchTheLock is the regression check issue #358 asks
// for, run against the real binary.
//
// Hold messages.db.lock, then invoke `wad --help` and an unknown command
// under a short deadline. Both must return promptly. The pre-#358 build
// blocked in flock indefinitely here — that is the bug, and the assertion
// is the deadline, not the exit code.
func TestUnknownArgsNeverTouchTheLock(t *testing.T) {
	root := t.TempDir()
	bin := filepath.Join(root, "wad")
	build := exec.CommandContext(t.Context(), "go", "build", "-o", bin, ".")
	build.Env = append(os.Environ(), "GOTOOLCHAIN=auto")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build wad: %v\n%s", err, out)
	}

	// An isolated XDG root so the daemon path would resolve stores here,
	// and a held lock so anything that reaches flock blocks.
	env := append(os.Environ(),
		"XDG_RUNTIME_DIR="+filepath.Join(root, "run"),
		"XDG_DATA_HOME="+filepath.Join(root, "data"),
		"XDG_STATE_HOME="+filepath.Join(root, "state"),
		"XDG_CONFIG_HOME="+filepath.Join(root, "config"),
	)
	lockDir := filepath.Join(root, "data", "wa", "default")
	if err := os.MkdirAll(lockDir, 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	// No skip: this file is unix-only, so flock exists. A failure here is
	// a real failure — silently skipping would retire the regression check
	// for a production incident.
	held, err := lockHeld(t, filepath.Join(lockDir, "messages.db.lock"))
	if err != nil {
		t.Fatalf("hold messages.db.lock: %v", err)
	}
	defer held()

	for _, args := range [][]string{{"--help"}, {"allow", "list"}, {"--porfile", "x"}} {
		t.Run(strings.Join(args, "_"), func(t *testing.T) {
			// A plain background ctx on purpose: the 10s select below IS
			// the assertion, and a context deadline here would turn a hang
			// into a tidy exit and hide the bug.
			cmd := exec.CommandContext(context.Background(), bin, args...)
			cmd.Env = env
			done := make(chan error, 1)
			if err := cmd.Start(); err != nil {
				t.Fatalf("start: %v", err)
			}
			go func() { done <- cmd.Wait() }()

			select {
			case <-done:
				// Returned promptly — never reached the lock.
			case <-time.After(10 * time.Second):
				_ = cmd.Process.Kill()
				t.Fatalf("wad %q did not return within 10s — it fell through to the composition root and blocked on the held lock", args)
			}
		})
	}
}

// lockHeld takes an exclusive flock on path and returns a release func.
func lockHeld(t *testing.T, path string) (func(), error) {
	t.Helper()
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	if err := flockExclusive(f); err != nil {
		_ = f.Close()
		return nil, err
	}
	return func() { _ = f.Close() }, nil
}
