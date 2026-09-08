package main

import (
	"fmt"
	"os"
	"strings"
)

// daemonUsage is what `wad --help` prints. It names the one-shot commands
// too, because the reason someone typed an unknown argument is almost
// always that they were looking for one of them.
const daemonUsage = `wad — the wa daemon.

usage:
  wad [--profile NAME] [--log-level LEVEL]   start the daemon (default)
  wad --help                                 this text

one-shot commands (never start a daemon):
  wad install-service [--profile NAME] [--dry-run]
  wad uninstall-service [--profile NAME]
  wad migrate [--profile NAME] [--dry-run] [--rollback]
  wad crash list
  wad reload
  wad audit rotate
  wad token issue|revoke|list|sweep

flags on the daemon path:
  --profile NAME     profile to run (also WA_PROFILE); default "default"
  --log-level LEVEL   debug|info|warn|error (also WA_LOG_LEVEL); default info

Client operations (send, allow, history, …) belong to the wa CLI, not wad.`

// argsProceed is the sentinel classifyDaemonArgs returns when the process
// should continue into the composition root.
const argsProceed = -1

// daemonValueFlags are the only flags the daemon composition root reads,
// and each takes a value. Kept as data so this gate and the parsers in
// parseLogLevel / resolveDaemonProfile cannot silently disagree about what
// is legal — a new daemon flag has to be added here or its own test fails.
var daemonValueFlags = map[string]struct{}{
	"--profile":   {},
	"--log-level": {},
}

// classifyDaemonArgs decides what an invocation that reached the daemon
// path should do: proceed, print usage, or refuse.
//
// It exists because `wad` used to treat EVERY unrecognised argument as
// "no arguments" and fall into the composition root. `wad --help`,
// `wad allow list` and plain typos therefore each started a second daemon,
// which blocked forever in flock on a messages.db.lock the real daemon
// already held — and `docker exec` timing out did not kill it. Eight such
// processes survived ~50 h on wa-burocracy holding ~5.5 MiB each, pushing
// a 128 MiB cgroup to 126.7 MiB with 711 reclaims (issue #358).
//
// Returns argsProceed to continue, or an exit code plus a message. Pure so
// the whole matrix is testable without spawning a process or touching a
// lock: refusing BEFORE any store is opened is the entire point, and a
// test that had to open one could not prove it.
func classifyDaemonArgs(args []string) (int, string) {
	for i := 0; i < len(args); i++ {
		arg := args[i]

		switch arg {
		case "-h", "--help", "help":
			return 0, daemonUsage
		}

		// `--flag=value` is self-contained; an empty value is still the
		// user naming the flag, and the downstream parsers treat it as
		// unset, so it is not an error here.
		if name, _, ok := strings.Cut(arg, "="); ok {
			if _, known := daemonValueFlags[name]; known {
				continue
			}
			return 2, fmt.Sprintf("wad: unknown flag %q\n\n%s", arg, daemonUsage)
		}

		if _, known := daemonValueFlags[arg]; known {
			if i+1 >= len(args) {
				return 2, fmt.Sprintf("wad: %s requires a value\n\n%s", arg, daemonUsage)
			}
			i++ // consume the value
			continue
		}

		// Anything left is either a mistyped flag or a client verb like
		// `allow` that belongs to the wa CLI. Both must refuse rather
		// than boot a daemon.
		return 2, fmt.Sprintf("wad: unknown argument %q\n\n%s", arg, daemonUsage)
	}
	return argsProceed, ""
}

// guardDaemonArgs applies classifyDaemonArgs to the real process arguments
// and exits when they are not a valid daemon invocation. Called after every
// one-shot handler has declined, so those keep their own argument grammar.
func guardDaemonArgs() {
	code, msg := classifyDaemonArgs(os.Args[1:])
	if code == argsProceed {
		return
	}
	out := os.Stderr
	if code == 0 {
		out = os.Stdout
	}
	_, _ = fmt.Fprintln(out, msg)
	os.Exit(code)
}
