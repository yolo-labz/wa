package main

import (
	"errors"
	"fmt"
	"strings"
)

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

// remoteUsageHint is shown alongside the parse error so operators see
// the correct shape inline.
func remoteUsageHint() string {
	return fmt.Sprintf("Example: --remote %s", "ProxMox.Dokku:wa-burocracy")
}
