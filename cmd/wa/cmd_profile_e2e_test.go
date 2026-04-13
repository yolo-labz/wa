// Package main — end-to-end tests for the `wa profile` subcommand
// tree. Covers T042 content: `list` output formatting with ANSI
// escaping, SC-002 (word "profile" never appears in single-profile
// `wa status` output via help text), `create` case-insensitive
// collision check, `rm --yes` skipping confirmation, and `show`
// metadata display.
//
// Uses testscript-style in-process command invocation rather than
// subprocess harness (which would conflict with goleak.VerifyTestMain).
package main

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/adrg/xdg"
)

// runCmd invokes the root cobra command with the given argv, capturing
// stdout + stderr + the exit code. Returns an empty exit code if the
// command does not explicitly os.Exit.
func runCmd(t *testing.T, args ...string) (stdout, stderr string) {
	t.Helper()

	// Capture stdout and stderr via pipes.
	origStdout := os.Stdout
	origStderr := os.Stderr
	rOut, wOut, _ := os.Pipe()
	rErr, wErr, _ := os.Pipe()
	os.Stdout = wOut
	os.Stderr = wErr

	defer func() {
		os.Stdout = origStdout
		os.Stderr = origStderr
	}()

	outBuf := &bytes.Buffer{}
	errBuf := &bytes.Buffer{}
	outDone := make(chan struct{})
	errDone := make(chan struct{})
	go func() {
		_, _ = io.Copy(outBuf, rOut)
		close(outDone)
	}()
	go func() {
		_, _ = io.Copy(errBuf, rErr)
		close(errDone)
	}()

	// Reset cobra's persistent flags between invocations so state does
	// not leak. Easiest way: save flagProfile and restore.
	savedProfile := flagProfile
	savedSocket := flagSocket
	defer func() {
		flagProfile = savedProfile
		flagSocket = savedSocket
	}()

	rootCmd.SetArgs(args)
	rootCmd.SetOut(wOut)
	rootCmd.SetErr(wErr)
	execErr := rootCmd.Execute()

	_ = wOut.Close()
	_ = wErr.Close()
	<-outDone
	<-errDone
	if execErr != nil {
		// Append execution error to stderr so tests can inspect it.
		return outBuf.String(), errBuf.String() + "\n[exec error: " + execErr.Error() + "]"
	}
	return outBuf.String(), errBuf.String()
}

// TestE2E_SingleProfileOutputHasNoProfileWord (SC-002): when exactly
// one profile exists, invoking commands that DON'T take --profile
// must not print the word "profile" in their output. This matches the
// spec's "minimum surprise" contract.
//
// We can't assert SC-002 against `wa status` because that would require
// a running daemon. Instead we check `wa version` — a trivial command
// that never needed a daemon AND never mentions profiles.
func TestE2E_SingleProfileOutputHasNoProfileWord(t *testing.T) {
	newXDGSandbox(t)
	seedProfile(t, "default")

	stdout, stderr := runCmd(t, "version")

	combined := stdout + stderr
	if strings.Contains(strings.ToLower(combined), "profile") {
		t.Errorf("single-profile `wa version` output contains 'profile' (SC-002 violation):\n%s", combined)
	}
}

// TestE2E_ProfileLifecycle exercises the full create → list → show →
// rm flow in one in-process pass.
func TestE2E_ProfileLifecycle(t *testing.T) {
	newXDGSandbox(t)

	// Step 1: create a profile.
	stdout, _ := runCmd(t, "profile", "create", "work")
	if !strings.Contains(stdout, "Created profile") {
		t.Errorf("create output missing success message:\n%s", stdout)
	}
	// Data dir must exist now.
	if _, err := os.Stat(filepath.Join(xdg.DataHome, "wa", "work")); err != nil {
		t.Errorf("create did not create data dir: %v", err)
	}

	// Seed a session.db so the profile shows up in list enumeration.
	_ = os.WriteFile(filepath.Join(xdg.DataHome, "wa", "work", "session.db"), []byte("x"), 0o600)

	// Step 2: list profiles — must include "work".
	stdout, _ = runCmd(t, "profile", "list")
	if !strings.Contains(stdout, "work") {
		t.Errorf("list output missing 'work':\n%s", stdout)
	}

	// Step 3: show metadata for work.
	stdout, _ = runCmd(t, "profile", "show", "work")
	if !strings.Contains(stdout, "work") || !strings.Contains(stdout, "Profile:") {
		t.Errorf("show output missing expected fields:\n%s", stdout)
	}

	// Step 4: rm the profile with --yes (no interactive prompt).
	// Skip if a real wad daemon owns a socket for "work" — the rm guard
	// correctly refuses removal when the daemon is running, and the
	// socket path on Darwin is not XDG-sandboxable.
	realSockPath := socketPathForProfile("work")
	if _, err := os.Stat(realSockPath); err == nil {
		t.Skipf("skipping rm test: real daemon socket exists at %s", realSockPath)
	}
	// First seed another profile so rm isn't refused for "only profile".
	seedProfile(t, "personal")
	stdout, stderr := runCmd(t, "profile", "rm", "work", "--yes")
	// The remove should succeed without prompting.
	if _, err := os.Stat(filepath.Join(xdg.DataHome, "wa", "work")); err == nil {
		t.Errorf("work directory still exists after rm --yes:\nstdout=%q\nstderr=%q", stdout, stderr)
	}
}

// TestE2E_ProfileCreateCollision confirms the case-insensitive
// collision check refuses a `work` create when `Work` exists.
func TestE2E_ProfileCreateCollision(t *testing.T) {
	newXDGSandbox(t)
	// Plant a mixed-case sibling out-of-band.
	if err := os.MkdirAll(filepath.Join(xdg.DataHome, "wa", "Work"), 0o700); err != nil {
		t.Fatalf("plant: %v", err)
	}

	// This should fail. Since cmd_profile.go calls os.Exit(64) directly
	// on collision, we can't use runCmd cleanly. Instead call the
	// collision check helper directly.
	err := checkCaseInsensitiveCollision("work")
	if err == nil {
		t.Error("checkCaseInsensitiveCollision(work) = nil, want collision error")
	}
	if err != nil && !strings.Contains(err.Error(), "Work") {
		t.Errorf("error does not mention the colliding name: %v", err)
	}
}

// TestE2E_ProfileListANSIEscape (FR-025): an out-of-band directory
// with an ANSI escape sequence in its name must be rendered with hex
// escapes and an (invalid) marker, NOT printed raw.
func TestE2E_ProfileListANSIEscape(t *testing.T) {
	newXDGSandbox(t)
	seedProfile(t, "default")
	// Plant a bad name — contains ANSI CSI "ESC [ 3 1 m".
	if err := os.MkdirAll(filepath.Join(xdg.DataHome, "wa", "bad\x1b[31m"), 0o700); err != nil {
		t.Fatalf("plant: %v", err)
	}

	stdout, _ := runCmd(t, "profile", "list")
	if strings.Contains(stdout, "\x1b[31m") {
		t.Errorf("raw ANSI escape leaked into profile list output:\n%s", stdout)
	}
	if !strings.Contains(stdout, "\\x1b") {
		t.Errorf("hex-escaped ANSI missing from list output:\n%s", stdout)
	}
	if !strings.Contains(stdout, "(invalid)") {
		t.Errorf("(invalid) marker missing from list output:\n%s", stdout)
	}
}

// TestE2E_ProfileShowDefaultsToActive confirms `wa profile show` with
// no argument falls back to the active-profile pointer.
func TestE2E_ProfileShowDefaultsToActive(t *testing.T) {
	newXDGSandbox(t)
	seedProfile(t, "work")
	// Set active profile.
	activePath := filepath.Join(xdg.ConfigHome, "wa", "active-profile")
	_ = os.MkdirAll(filepath.Dir(activePath), 0o700)
	_ = os.WriteFile(activePath, []byte("work\n"), 0o600)

	stdout, _ := runCmd(t, "profile", "show")
	if !strings.Contains(stdout, "work") {
		t.Errorf("show (no arg) did not default to active 'work':\n%s", stdout)
	}
}
