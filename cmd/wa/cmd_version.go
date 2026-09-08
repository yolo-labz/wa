package main

import (
	"fmt"
	"io"
	"os"
	"runtime/debug"

	"github.com/yolo-labz/wa/v2/internal/buildstamp"

	"github.com/spf13/cobra"
)

// version is set by ldflags at build time:
//
//	go build -ldflags "-X main.version=v0.1.0" ./cmd/wa
//
// When the binary is built via `go install` (no ldflags), version
// stays at "dev" — but Go 1.18+ records the module version inside
// `runtime/debug.BuildInfo`. resolveVersion() prefers the ldflag if
// set, otherwise falls back to the module version, otherwise "dev".
var version = "dev"

// commit and date are set by the same ldflags that set version:
//
//	-X main.commit=<sha> -X main.date=<rfc3339>
//
// They MUST exist even though nothing but the banner reads them. Both
// build systems have always passed these two flags for ./cmd/wa
// (.goreleaser.yaml and the Dockerfile), and the Go linker silently
// ignores an -X naming a symbol the package does not declare — so every
// wa binary ever shipped reported a version and no commit, and confirming
// what was actually deployed meant `docker cp` plus `strings` on the
// binary. Issue #365. TestLdflagTargetsExist keeps them wired.
var (
	commit = ""
	date   = ""
)

func resolveVersion() string {
	if version != "dev" {
		return version
	}
	if info, ok := debug.ReadBuildInfo(); ok && info.Main.Version != "" && info.Main.Version != "(devel)" {
		return info.Main.Version
	}
	return version
}

// printVersion writes the version banner to w. Shared between the
// `wa version` subcommand and the root-level `--version` flag, which
// main.go intercepts via argv scan BEFORE cobra subcommand routing so
// it works on bare root, any subcommand, and on builds whose subcommand
// list has drifted (spec 110i FR-001 forward compat).
func printVersion(w io.Writer, useJSON bool) {
	v := resolveVersion()
	if useJSON {
		// Backtick raw string keeps the literal `"wa.version/v1"`
		// discoverable to the schema-drift guard in
		// internal/app/schemas_golden_test.go (which greps source
		// for `"wa.X/vN"` patterns and would miss \"-escaped forms).
		// commit and date are ADDITIVE and omitted when unset, so the
		// v1 shape is unchanged for anything already parsing this and
		// no schema bump is required (FR-004). A consumer confirming a
		// canary rollout can now compare a SHA instead of a
		// `git describe` string, which is the whole point of #365.
		_, _ = fmt.Fprintf(w, `{"schema":"wa.version/v1","version":%q`, v)
		if commit != "" {
			_, _ = fmt.Fprintf(w, `,"commit":%q`, commit)
		}
		if date != "" {
			_, _ = fmt.Fprintf(w, `,"date":%q`, date)
		}
		_, _ = fmt.Fprint(w, "}")
		_, _ = fmt.Fprintln(w)
		return
	}
	_, _ = fmt.Fprintf(w, "wa version %s\n", buildstamp.Banner(v, commit, date))
}

var versionCmd = &cobra.Command{
	Use:   "version",
	Short: "Print the wa CLI version",
	Run: func(cmd *cobra.Command, args []string) {
		printVersion(os.Stdout, flagJSON)
	},
}

func init() {
	// Register `--version` as a persistent flag so `wa --help` lists it.
	// Actual handling lives in main.go (argv pre-scan); cobra never sees
	// this flag in practice because main.go exits before Execute when
	// --version is present.
	rootCmd.PersistentFlags().Bool("version", false, "print the wa CLI version and exit")
}
