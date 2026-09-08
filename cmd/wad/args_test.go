package main

import (
	"strings"
	"testing"
)

// TestClassifyDaemonArgs is the decision matrix. Issue #358: every one of
// the "refuse" rows used to fall through into the composition root and
// start a second daemon.
func TestClassifyDaemonArgs(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		args []string
		want int
	}{
		// Proceed — the daemon's real invocations.
		{"bare wad", nil, argsProceed},
		{"profile space form", []string{"--profile", "burocracy"}, argsProceed},
		{"profile equals form", []string{"--profile=burocracy"}, argsProceed},
		{"log-level space form", []string{"--log-level", "debug"}, argsProceed},
		{"log-level equals form", []string{"--log-level=debug"}, argsProceed},
		{"both flags", []string{"--profile", "p", "--log-level", "warn"}, argsProceed},
		{"both equals", []string{"--profile=p", "--log-level=warn"}, argsProceed},
		{"mixed forms", []string{"--profile=p", "--log-level", "warn"}, argsProceed},

		// Usage — must exit 0, not boot.
		{"--help", []string{"--help"}, 0},
		{"-h", []string{"-h"}, 0},
		{"help verb", []string{"help"}, 0},
		{"help after a valid flag", []string{"--profile", "p", "--help"}, 0},

		// Refuse — the failure modes from the incident.
		{"client verb", []string{"allow", "list"}, 2},
		{"typo flag", []string{"--porfile", "p"}, 2},
		{"unknown equals flag", []string{"--porfile=p"}, 2},
		{"bare positional", []string{"status"}, 2},
		{"value flag with no value", []string{"--profile"}, 2},
		{"log-level with no value", []string{"--log-level"}, 2},
		{"unknown after valid", []string{"--profile", "p", "send"}, 2},
		{"single dash", []string{"-x"}, 2},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, msg := classifyDaemonArgs(tc.args)
			if got != tc.want {
				t.Fatalf("classifyDaemonArgs(%q) = %d, want %d (msg: %s)", tc.args, got, tc.want, msg)
			}
			if tc.want != argsProceed && msg == "" {
				t.Error("a non-proceed decision must carry a message")
			}
			if tc.want == argsProceed && msg != "" {
				t.Errorf("proceed must not carry a message, got %q", msg)
			}
		})
	}
}

// TestClassifyNamesTheOffendingArgument — the operator has to learn WHICH
// argument was wrong. A bare "invalid usage" would send them back to
// guessing, which is how the incident lasted 50 hours.
func TestClassifyNamesTheOffendingArgument(t *testing.T) {
	t.Parallel()
	_, msg := classifyDaemonArgs([]string{"--porfile", "p"})
	if !strings.Contains(msg, "--porfile") {
		t.Errorf("message does not name the bad flag: %q", msg)
	}
	_, msg = classifyDaemonArgs([]string{"allow", "list"})
	if !strings.Contains(msg, "allow") {
		t.Errorf("message does not name the bad argument: %q", msg)
	}
}

// TestDaemonValueFlagsMatchTheParsers is the anti-drift guard. The gate
// keeps its own list of legal flags; if a future daemon flag is read by
// parseLogLevel / resolveDaemonProfile but never added here, the gate
// would refuse a legitimate invocation. Assert the two known parsers'
// flags are exactly the gate's set.
func TestDaemonValueFlagsMatchTheParsers(t *testing.T) {
	t.Parallel()
	for _, f := range []string{"--profile", "--log-level"} {
		if _, ok := daemonValueFlags[f]; !ok {
			t.Errorf("%s is parsed on the daemon path but the gate would refuse it", f)
		}
	}
	if len(daemonValueFlags) != 2 {
		t.Errorf("daemonValueFlags has %d entries; add the new flag's parser test too", len(daemonValueFlags))
	}
}
