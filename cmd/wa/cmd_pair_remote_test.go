package main

import (
	"errors"
	"os"
	"os/exec"
	"strings"
	"testing"
)

// TestParseRemoteTarget walks every row of data-model.md §Test
// coverage matrix. Feature 110e FR-002 + FR-003.
func TestParseRemoteTarget(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name          string
		input         string
		wantHost      string
		wantApp       string
		wantErr       bool
		wantExitCode  int
		wantErrSubstr string
	}{
		{
			name:     "HappyPath",
			input:    "ProxMox.Dokku:wa-burocracy",
			wantHost: "ProxMox.Dokku",
			wantApp:  "wa-burocracy",
		},
		{
			name:     "UserAtHost",
			input:    "pedro@host.example:wa-personal",
			wantHost: "pedro@host.example",
			wantApp:  "wa-personal",
		},
		{
			name:     "MultiColonApp",
			input:    "host:app:extra",
			wantHost: "host",
			wantApp:  "app:extra",
		},
		{
			name:          "EmptyRejected",
			input:         "",
			wantErr:       true,
			wantExitCode:  64,
			wantErrSubstr: "empty string",
		},
		{
			name:          "MissingSeparator",
			input:         "no-colon",
			wantErr:       true,
			wantExitCode:  64,
			wantErrSubstr: "missing ':' separator",
		},
		{
			name:          "EmptyHost",
			input:         ":app",
			wantErr:       true,
			wantExitCode:  64,
			wantErrSubstr: "empty host",
		},
		{
			name:          "EmptyApp",
			input:         "host:",
			wantErr:       true,
			wantExitCode:  64,
			wantErrSubstr: "empty app",
		},
		{
			name:          "URLRejectedHTTPS",
			input:         "https://wa.example.com",
			wantErr:       true,
			wantExitCode:  64,
			wantErrSubstr: "pair requires SSH access",
		},
		{
			name:          "URLRejectedHTTP",
			input:         "http://wa.example.com",
			wantErr:       true,
			wantExitCode:  64,
			wantErrSubstr: "pair requires SSH access",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := ParseRemoteTarget(tt.input)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("expected error for input %q, got nil", tt.input)
				}
				rpe := asRemoteParseError(err)
				if rpe == nil {
					t.Fatalf("expected *remoteParseError, got %T: %v", err, err)
				}
				if rpe.ExitCode() != tt.wantExitCode {
					t.Errorf("ExitCode = %d, want %d", rpe.ExitCode(), tt.wantExitCode)
				}
				if !strings.Contains(rpe.Error(), tt.wantErrSubstr) {
					t.Errorf("Error %q does not contain %q", rpe.Error(), tt.wantErrSubstr)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error for input %q: %v", tt.input, err)
			}
			if got.Host != tt.wantHost {
				t.Errorf("Host = %q, want %q", got.Host, tt.wantHost)
			}
			if got.App != tt.wantApp {
				t.Errorf("App = %q, want %q", got.App, tt.wantApp)
			}
		})
	}
}

// TestPairHelpShowsRemoteFlag asserts the `--remote` flag is
// discoverable in `wa pair --help`. FR-001 + SC-003.
func TestPairHelpShowsRemoteFlag(t *testing.T) {
	t.Parallel()
	help := pairCmd.UsageString()
	for _, want := range []string{
		"--remote",
		"ProxMox.Dokku:wa-burocracy",
	} {
		if !strings.Contains(help, want) {
			t.Errorf("pair --help missing %q.\nUsage was:\n%s", want, help)
		}
	}
}

// captureExecCommand swaps execCommand for a fake that records every
// invocation and returns a no-op `true` command. Restored on
// t.Cleanup so parallel tests in the same package are not perturbed.
// Note: TestParseRemoteTarget is t.Parallel but does not touch
// execCommand, so this helper is safe to use in non-parallel argv
// tests.
func captureExecCommand(t *testing.T) *[]struct {
	Name string
	Args []string
} {
	t.Helper()
	var calls []struct {
		Name string
		Args []string
	}
	prev := execCommand
	execCommand = func(name string, args ...string) *exec.Cmd {
		calls = append(calls, struct {
			Name string
			Args []string
		}{Name: name, Args: append([]string(nil), args...)})
		// Return a /bin/true command so cmd.Run() succeeds without
		// invoking the real ssh binary.
		return exec.Command("true")
	}
	t.Cleanup(func() { execCommand = prev })
	return &calls
}

// TestRunPairRemote_ArgvShape covers FR-001 + FR-005: the SSH chain
// receives positional argv matching `ssh -t <host> dokku enter <app>
// -- /usr/local/bin/wa pair <extras...>`. Five flag combinations.
func TestRunPairRemote_ArgvShape(t *testing.T) {
	// Resets to a known baseline before each row because the cobra
	// flag-state pairPhone / pairBrowser / pairIdempotencyKey / flagJSON
	// is package-level.
	savePhone, saveBrowser, saveIdem, saveJSON := pairPhone, pairBrowser, pairIdempotencyKey, flagJSON
	t.Cleanup(func() {
		pairPhone, pairBrowser, pairIdempotencyKey, flagJSON = savePhone, saveBrowser, saveIdem, saveJSON
	})

	tests := []struct {
		name   string
		setup  func()
		extras []string
	}{
		{
			name:   "Bare",
			setup:  func() { pairPhone, pairBrowser, pairIdempotencyKey, flagJSON = "", false, "", false },
			extras: nil,
		},
		{
			name:   "Phone",
			setup:  func() { pairPhone, pairBrowser, pairIdempotencyKey, flagJSON = "+5511999999999", false, "", false },
			extras: []string{"--phone", "+5511999999999"},
		},
		{
			name:   "Browser",
			setup:  func() { pairPhone, pairBrowser, pairIdempotencyKey, flagJSON = "", true, "", false },
			extras: []string{"--browser"},
		},
		{
			name:   "IdempotencyKey",
			setup:  func() { pairPhone, pairBrowser, pairIdempotencyKey, flagJSON = "", false, "abc123", false },
			extras: []string{"--idempotency-key", "abc123"},
		},
		{
			name:   "AllCombined",
			setup:  func() { pairPhone, pairBrowser, pairIdempotencyKey, flagJSON = "+5511999999999", true, "abc123", true },
			extras: []string{"--phone", "+5511999999999", "--browser", "--idempotency-key", "abc123", "--json"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.setup()
			calls := captureExecCommand(t)
			target := RemoteTarget{Host: "ProxMox.Dokku", App: "wa-burocracy"}
			extras := buildPairExtraFlags()
			if exit, err := runPairRemote(target, extras); err != nil {
				t.Fatalf("runPairRemote: unexpected error: %v (exit %d)", err, exit)
			}
			if len(*calls) != 1 {
				t.Fatalf("execCommand calls = %d, want 1", len(*calls))
			}
			got := (*calls)[0]
			if got.Name != "ssh" {
				t.Errorf("Name = %q, want %q", got.Name, "ssh")
			}
			wantArgs := append([]string{
				"-t", "ProxMox.Dokku", "dokku", "enter", "wa-burocracy", "--",
				"/usr/local/bin/wa", "pair",
			}, tt.extras...)
			if !equalStringSlices(got.Args, wantArgs) {
				t.Errorf("Args mismatch.\n  got:  %q\n  want: %q", got.Args, wantArgs)
			}
		})
	}
}

func equalStringSlices(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// TestRunPairRemote_SSHMissing covers FR-006: ssh-binary missing yields
// exit code 70 (sysexits EX_SOFTWARE) and an actionable message.
func TestRunPairRemote_SSHMissing(t *testing.T) {
	// Swap the lookPath seam to always fail.
	prev := lookPath
	t.Cleanup(func() { lookPath = prev })
	lookPath = func(_ string) (string, error) {
		return "", &exec.Error{Name: "ssh", Err: errSSHMissingForTest}
	}

	// Also wire execCommand to fail loudly if reached — runPairRemote
	// must short-circuit before execCommand.
	prevExec := execCommand
	t.Cleanup(func() { execCommand = prevExec })
	execCommand = func(name string, args ...string) *exec.Cmd {
		t.Fatalf("execCommand reached despite ssh-missing pre-flight: %s %v", name, args)
		return exec.Command("false")
	}

	target := RemoteTarget{Host: "ProxMox.Dokku", App: "wa-burocracy"}
	exit, err := runPairRemote(target, nil)
	if exit != 70 {
		t.Errorf("exit = %d, want 70", exit)
	}
	rpe := asRemoteParseError(err)
	if rpe == nil {
		t.Fatalf("expected *remoteParseError, got %T: %v", err, err)
	}
	if rpe.ExitCode() != 70 {
		t.Errorf("rpe.ExitCode = %d, want 70", rpe.ExitCode())
	}
	if !strings.Contains(rpe.Error(), "ssh binary not found") {
		t.Errorf("error message missing 'ssh binary not found': %q", rpe.Error())
	}
}

// errSSHMissingForTest is a sentinel that the lookPath fake returns.
// Not used elsewhere.
var errSSHMissingForTest = errors.New("simulated missing ssh")

// stderrDiscard is a discardable stderr placeholder for tests that
// need to silence stderr without inspecting it. Not used yet; reserved
// for future tests that exercise the SSH chain with real exec.
var _ = os.Stderr
