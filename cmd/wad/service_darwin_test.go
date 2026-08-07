//go:build darwin

package main

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"testing"
)

// fakeLaunchctl builds an execCommandContext replacement. exitCodes maps
// the launchctl subcommand ("bootstrap"/"bootout") to the exit code its
// invocation should return, in call order per subcommand — e.g.
// exitCodes["bootstrap"] = []int{1, 0} means the first bootstrap call
// exits 1, the second (the retry) exits 0. A missing or exhausted list
// exits 0. This drives *exec.Cmd.Run() through a real subprocess (the
// standard os/exec TestHelperProcess pattern) so no launchctl binary is
// invoked and the seam is exercised exactly as production code calls it.
func fakeLaunchctl(t *testing.T, exitCodes map[string][]int) func(ctx context.Context, name string, args ...string) *exec.Cmd {
	t.Helper()
	calls := map[string]int{}
	return func(ctx context.Context, name string, args ...string) *exec.Cmd {
		if name != "launchctl" || len(args) == 0 {
			t.Fatalf("unexpected command: %s %v", name, args)
		}
		sub := args[0]
		idx := calls[sub]
		calls[sub]++
		code := 0
		if codes, ok := exitCodes[sub]; ok && idx < len(codes) {
			code = codes[idx]
		}
		cs := []string{"-test.run=TestHelperProcess", "--", strconv.Itoa(code)}
		cmd := exec.CommandContext(ctx, os.Args[0], cs...)
		cmd.Env = []string{"GO_WANT_HELPER_PROCESS=1"}
		return cmd
	}
}

// TestHelperProcess is not a real test; it is the subprocess body that
// fakeLaunchctl's *exec.Cmd actually runs. It exits with the code passed
// as the last argv entry. Standard os/exec fake-command pattern.
func TestHelperProcess(t *testing.T) {
	if os.Getenv("GO_WANT_HELPER_PROCESS") != "1" {
		return
	}
	args := os.Args
	code := 0
	if len(args) > 0 {
		if n, err := parseExitCode(args[len(args)-1]); err == nil {
			code = n
		}
	}
	os.Exit(code)
}

func parseExitCode(s string) (int, error) {
	n := 0
	for _, r := range s {
		if r < '0' || r > '9' {
			return 0, errors.New("not a digit string")
		}
		n = n*10 + int(r-'0')
	}
	return n, nil
}

// TestInstallServiceFor_BootstrapAlreadyLoaded pins the idempotent path:
// first bootstrap fails (already loaded), bootout runs, retry succeeds —
// installServiceFor must return nil, and a second `install-service` run
// stays a no-op success.
func TestInstallServiceFor_BootstrapAlreadyLoaded(t *testing.T) {
	orig := execCommandContext
	t.Cleanup(func() { execCommandContext = orig })
	execCommandContext = fakeLaunchctl(t, map[string][]int{
		"bootstrap": {1, 0}, // first fails, retry succeeds
	})

	dir := t.TempDir()
	t.Setenv("HOME", dir)

	if err := installServiceFor("default", "<plist/>"); err != nil {
		t.Fatalf("installServiceFor() = %v, want nil (already-loaded path is idempotent)", err)
	}
}

// TestInstallServiceFor_FirstErrorSurfacesWhenRetryAlsoFails is the RED
// case for SF-06 (cmd/wad/service_darwin.go:174-179 before the fix): both
// bootstrap attempts fail with DIFFERENT underlying causes. The returned
// error must be traceable to the FIRST attempt, not just say "retry
// failed" — a caller diagnosing "bad plist" vs "SIP refusal" needs the
// causal error, and CLAUDE.md R30 forbids discarding it in favour of
// whatever the retry says.
func TestInstallServiceFor_FirstErrorSurfacesWhenRetryAlsoFails(t *testing.T) {
	orig := execCommandContext
	t.Cleanup(func() { execCommandContext = orig })
	execCommandContext = fakeLaunchctl(t, map[string][]int{
		"bootstrap": {17, 5}, // distinct exit codes: first != retry
	})

	dir := t.TempDir()
	t.Setenv("HOME", dir)

	err := installServiceFor("default", "<plist/>")
	if err == nil {
		t.Fatal("installServiceFor() = nil, want an error when both bootstrap attempts fail")
	}

	// The first attempt's *exec.ExitError (exit status 17) must be
	// reachable via errors.Is/As on the returned error — proving the
	// causal failure was joined in, not shadowed by the retry's exit
	// status 5. errors.Join preserves both leaves for errors.As to walk.
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) {
		t.Fatalf("returned error has no *exec.ExitError in its tree: %v", err)
	}
	// Both exit codes (17 from the first attempt, 5 from the retry) must
	// appear somewhere in the joined error text — this is the falsifier:
	// before the fix, only "exit status 5" (the retry) would be present.
	msg := err.Error()
	if !strings.Contains(msg, "exit status 17") {
		t.Errorf("error %q does not mention the FIRST bootstrap's exit status 17 (SF-06: first decisive error was discarded)", msg)
	}
	if !strings.Contains(msg, "exit status 5") {
		t.Errorf("error %q does not mention the retry's exit status 5", msg)
	}
}
