// Package main is the wad daemon composition root.
//
// resolveDaemonProfile resolves the active profile for a wad daemon
// process from its CLI arguments and environment. This is a minimal
// precedence chain — the full four-source chain (FR-001) lives in
// cmd/wa/profile.go for the CLI client. wad is a daemon, not a CLI, so
// it only needs --profile / WA_PROFILE / "default" fallback.
package main

import (
	"os"
	"strings"
)

// resolveDaemonProfile returns the profile name for this wad process.
// Precedence:
//  1. --profile <name> flag (before any other --flag parsing)
//  2. WA_PROFILE env var
//  3. literal "default"
//
// Empty-string sources are treated as unset (FR-001), so
// `WA_PROFILE=""` falls through to "default".
func resolveDaemonProfile() string {
	// (1) --profile flag, simple walk of os.Args (no cobra on the daemon side).
	for i, arg := range os.Args[1:] {
		if arg == "--profile" && i+1 < len(os.Args)-1 {
			name := strings.TrimSpace(os.Args[i+2])
			if name != "" {
				return name
			}
		}
		if after, ok := strings.CutPrefix(arg, "--profile="); ok {
			name := strings.TrimSpace(after)
			if name != "" {
				return name
			}
		}
	}

	// (2) WA_PROFILE env var (empty treated as unset).
	if env := strings.TrimSpace(os.Getenv("WA_PROFILE")); env != "" {
		return env
	}

	// (3) default.
	return DefaultProfile
}
