package main

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// execCommand is the test seam over os/exec.Command. Tests swap it for
// a fake that captures argv without invoking a real process.
// T2-01 / FR-001.
var execCommand = exec.Command

// lookPath is the test seam over exec.LookPath. Tests swap it to
// simulate a missing `ssh` binary. T2-04 / FR-006.
var lookPath = exec.LookPath

// RemoteTarget names a remote daemon by its SSH host and dokku app.
// Lives in package main under cmd/wa. Pair-remote is a CLI-layer
// concern (FR-007 forbids daemon-side change for feature 110e).
type RemoteTarget struct {
	Host string // SSH destination — anything `ssh` resolves (hostname, ~/.ssh/config alias, user@host, Tailscale name).
	App  string // dokku app name (e.g. "wa-burocracy"). Used in `dokku enter <App>`.
}

// remoteParseError carries the operator-facing message AND the
// sysexits-style exit code. cmd_pair.go's RunE pipes the error
// through exiterr() which honours ExitCode().
type remoteParseError struct {
	Code    int
	Message string
}

func (e *remoteParseError) Error() string { return e.Message }

// ExitCode satisfies the interface that cmd/wa/output.go's exiterr
// inspects to map errors to sysexits codes.
func (e *remoteParseError) ExitCode() int { return e.Code }

// ParseRemoteTarget validates and splits a `<host>:<app>` flag value.
// Returns sysexits 64 (EX_USAGE) on any malformed input or on a
// URL-shaped value (FR-003).
func ParseRemoteTarget(s string) (RemoteTarget, error) {
	if s == "" {
		return RemoteTarget{}, &remoteParseError{Code: 64, Message: "wa pair --remote: expected <host>:<app>, got empty string"}
	}
	// Refuse URL form first — operators sometimes paste the daemon's
	// REST endpoint by analogy with the 110c `--remote` flag on other
	// subcommands. Pair needs SSH, not HTTP. Spec FR-003.
	if strings.HasPrefix(s, "http://") || strings.HasPrefix(s, "https://") {
		return RemoteTarget{}, &remoteParseError{
			Code: 64,
			Message: "wa pair --remote: pair requires SSH access to the daemon's host, not the REST endpoint. " +
				"Use --remote <ssh-host>:<dokku-app> instead — e.g. --remote ProxMox.Dokku:wa-burocracy.",
		}
	}
	host, app, ok := strings.Cut(s, ":")
	if !ok {
		return RemoteTarget{}, &remoteParseError{Code: 64, Message: "wa pair --remote: expected <host>:<app>, got missing ':' separator"}
	}
	if host == "" {
		return RemoteTarget{}, &remoteParseError{Code: 64, Message: "wa pair --remote: expected <host>:<app>, got empty host"}
	}
	if app == "" {
		return RemoteTarget{}, &remoteParseError{Code: 64, Message: "wa pair --remote: expected <host>:<app>, got empty app"}
	}
	return RemoteTarget{Host: host, App: app}, nil
}

// errRemoteParse is the sentinel matched by errors.As to recover
// the structured exit code in the cobra RunE. Tests use this to
// assert error shape without depending on string matching.
var errRemoteParse = &remoteParseError{}

// asRemoteParseError unwraps to *remoteParseError or returns nil.
// Helper for tests and the RunE error path.
func asRemoteParseError(err error) *remoteParseError {
	var rpe *remoteParseError
	if errors.As(err, &rpe) {
		return rpe
	}
	return nil
}

// remoteWadPath is the in-container path of the `wa` binary. Mirrors
// Dockerfile (`COPY --chown=65532:65532 /out/wa /usr/local/bin/wa`).
// Hard-coded because the dokku-enter chain runs INSIDE the container,
// not against the operator's filesystem.
const remoteWadPath = "/usr/local/bin/wa"

// buildPairExtraFlags collects the existing pair flags into the argv
// passed through the SSH chain. Order is stable (test-friendly).
// Spec FR-005 + 110f --reset passthrough.
func buildPairExtraFlags() []string {
	out := make([]string, 0, 8)
	if pairReset {
		out = append(out, "--reset")
	}
	if pairPhone != "" {
		out = append(out, "--phone", pairPhone)
	}
	if pairBrowser {
		out = append(out, "--browser")
	}
	if pairIdempotencyKey != "" {
		out = append(out, "--idempotency-key", pairIdempotencyKey)
	}
	if flagJSON {
		out = append(out, "--json")
	}
	return out
}

// remoteExitError carries a passthrough exit code from the SSH chain.
// SSH exits 255 on connection failure; `dokku enter` propagates the
// in-container `wa pair` exit code. Either way, the operator sees the
// outer ssh exit. Spec FR-006 + contracts/cli-flag.md exit-code table.
type remoteExitError struct {
	Code int
	Err  error
}

func (e *remoteExitError) Error() string {
	if e.Err != nil {
		return e.Err.Error()
	}
	return fmt.Sprintf("wa pair --remote: ssh chain exited %d", e.Code)
}

func (e *remoteExitError) ExitCode() int { return e.Code }
func (e *remoteExitError) Unwrap() error { return e.Err }

// runPairRemote wraps the SSH + dokku-enter chain. Stdin / stdout /
// stderr are inherited so the operator's terminal sees the QR
// half-block render and any prompts. Returns the exit code SSH (or
// the chain) produced, or remoteParseError{Code: 70} when `ssh` is
// absent from PATH. Spec FR-001 + FR-005 + FR-006.
func runPairRemote(target RemoteTarget, extraFlags []string) (int, error) {
	if _, err := lookPath("ssh"); err != nil {
		return 70, &remoteParseError{
			Code: 70,
			Message: "wa pair --remote: ssh binary not found in PATH. " +
				"Install OpenSSH client (e.g. `brew install openssh` on darwin, " +
				"`apt-get install openssh-client` on debian) or fix PATH.",
		}
	}

	// Argv assembled positionally. exec.Command does NOT invoke a
	// shell, so `+` and other metacharacters inside `--phone +5511...`
	// reach the in-container `wa pair` untouched. DR-005. Preallocate
	// because we know the fixed-base length (8) up front.
	argv := make([]string, 0, 8+len(extraFlags))
	argv = append(argv,
		"-t",
		target.Host,
		"dokku",
		"enter",
		target.App,
		"--",
		remoteWadPath,
		"pair",
	)
	argv = append(argv, extraFlags...)

	cmd := execCommand("ssh", argv...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	err := cmd.Run()
	if err == nil {
		return 0, nil
	}
	// Surface SSH's own exit code untranslated so operators see SSH's
	// 255 on connection failure or dokku's 1 on missing-app etc.
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return exitErr.ExitCode(), &remoteExitError{Code: exitErr.ExitCode(), Err: err}
	}
	// Non-ExitError path (binary not found mid-flight, signal, etc.).
	return 1, &remoteExitError{Code: 1, Err: err}
}
