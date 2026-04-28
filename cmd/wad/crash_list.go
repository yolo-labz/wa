package main

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/yolo-labz/wa/v2/internal/observability"
)

// handleCrashCommand dispatches `wad crash …` subcommands. Returns true
// if os.Args[1] was a crash command and the process should not continue
// into the daemon main loop.
func handleCrashCommand() (handled bool) {
	if len(os.Args) < 2 || os.Args[1] != "crash" {
		return false
	}
	if len(os.Args) < 3 {
		fmt.Fprintln(os.Stderr, "usage: wad crash list [--profile <name>] [--json]")
		os.Exit(64) // EX_USAGE
	}
	switch os.Args[2] {
	case "list":
		os.Exit(runCrashList())
	default:
		fmt.Fprintf(os.Stderr, "wad crash: unknown subcommand %q\n", os.Args[2])
		os.Exit(64)
	}
	return true
}

// runCrashList lists the crash dumps for the resolved profile.
// Output is a plain table unless --json is passed.
func runCrashList() int {
	profile := parseServiceProfileFlag()
	resolver, err := NewPathResolver(profile)
	if err != nil {
		fmt.Fprintf(os.Stderr, "wad crash list: profile %q: %v\n", profile, err)
		return 78
	}

	crashes, err := observability.ListCrashes(resolver.CrashesDir())
	if err != nil {
		fmt.Fprintf(os.Stderr, "wad crash list: %v\n", err)
		return 1
	}

	if hasBoolFlag("--json") {
		enc := json.NewEncoder(os.Stdout)
		enc.SetEscapeHTML(false)
		payload := struct {
			Schema  string                    `json:"schema"`
			Profile string                    `json:"profile"`
			Dir     string                    `json:"dir"`
			Crashes []observability.CrashInfo `json:"crashes"`
		}{
			Schema:  "wa.crash.list/v1",
			Profile: resolver.Profile(),
			Dir:     resolver.CrashesDir(),
			Crashes: crashes,
		}
		if err := enc.Encode(payload); err != nil {
			fmt.Fprintf(os.Stderr, "wad crash list: encode: %v\n", err)
			return 1
		}
		return 0
	}

	if len(crashes) == 0 {
		fmt.Printf("no crash dumps in %s\n", resolver.CrashesDir())
		return 0
	}
	fmt.Printf("%-30s %10s  %s\n", "TIME (UTC)", "BYTES", "NAME")
	for _, c := range crashes {
		fmt.Printf("%-30s %10d  %s\n", c.ModTime.UTC().Format("2006-01-02T15:04:05Z"), c.Size, c.Name)
	}
	return 0
}
