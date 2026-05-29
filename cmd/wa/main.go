// Package main is the wa thin CLI client. Every subcommand is a single
// JSON-RPC call against the wad daemon over a unix socket. No use case
// logic lives here.
package main

import (
	"errors"
	"fmt"
	"os"
	"strings"
)

func main() {
	os.Exit(run())
}

func run() int {
	// Spec 110i FR-001: intercept `--version` BEFORE cobra subcommand
	// routing. This is the only way to handle bare root, any subcommand,
	// AND drifted subcommands (forward compat). cobra's built-in
	// rootCmd.Version path only fires on the root command and would
	// trigger after subcommand parsing — too late for an unknown
	// subcommand like `wa drifted-subcmd --version`.
	if argvHasFlag(os.Args[1:], "version") {
		printVersion(os.Stdout, argvHasFlag(os.Args[1:], "json"))
		return 0
	}

	err := rootCmd.Execute()
	if err == nil {
		return 0
	}
	fmt.Fprintln(os.Stderr, err)

	var ee *exitError
	isExit := errors.As(err, &ee)

	// Actionable remediation hint. A daemon refusal carries an rpc code we
	// can map to a concrete next step; a bare exit-10 with no rpc code
	// means the socket dial itself failed — i.e. the daemon is not
	// running. Extends the `wa doctor` actionable-diagnostics ethos to
	// every command.
	if code, ok := rpcErrCode(err); ok {
		if h := hintForRPCCode(code); h != "" {
			fmt.Fprintln(os.Stderr, "hint:", h)
		}
	} else if isExit && ee.ExitCode() == exitUnavailable {
		fmt.Fprintln(os.Stderr, "hint: is the daemon running? start it with `wad` (or `wad install-service`)")
	}

	// Spec 110i FR-003: append stale-build footer when cobra rejects
	// an unknown flag or command. Other errors (network, JSON-RPC, etc.)
	// pass through unchanged so log noise stays low.
	if hint := appendStaleHint(err); hint != "" {
		fmt.Fprintln(os.Stderr, hint)
	}

	if isExit {
		return ee.ExitCode()
	}
	return 1
}

// rpcErrCode extracts the JSON-RPC error code from a daemon failure when
// the error chain carries one (exiterr wraps the *rpcError returned by
// callAndClose / the subscribe path).
func rpcErrCode(err error) (int, bool) {
	var re *rpcError
	if errors.As(err, &re) {
		return re.Code, true
	}
	return 0, false
}

// argvHasFlag scans argv for a long bool flag, honoring the POSIX `--`
// terminator. Matches `--name`, `--name=true`, `--name=1`. Does NOT
// match `--name=false`/`--name=0` or anything after a literal `--`.
func argvHasFlag(args []string, name string) bool {
	target := "--" + name
	targetEq := target + "="
	for _, a := range args {
		if a == "--" {
			return false
		}
		if a == target {
			return true
		}
		if strings.HasPrefix(a, targetEq) {
			val := strings.TrimPrefix(a, targetEq)
			return val != "false" && val != "0"
		}
	}
	return false
}

// appendStaleHint returns the footer string to print after a cobra
// "unknown flag" or "unknown command" error, or empty string for any
// other error. The footer tells the user their build may be stale and
// names the upgrade command for their install method. Thin wrapper
// over appendStaleHintFor for production wiring.
func appendStaleHint(err error) string {
	return appendStaleHintFor(err, resolveVersion(), upgradeHint())
}

// appendStaleHintFor is the pure logic, separated for testability.
func appendStaleHintFor(err error, ver, hint string) string {
	if err == nil {
		return ""
	}
	msg := err.Error()
	if !strings.Contains(msg, "unknown flag") && !strings.Contains(msg, "unknown command") {
		return ""
	}
	return fmt.Sprintf(
		"Build %s. If the flag/command is documented but unrecognized, your build may be stale.\nUpgrade: %s",
		ver, hint,
	)
}
