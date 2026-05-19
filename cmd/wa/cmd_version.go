package main

import (
	"fmt"
	"io"
	"os"
	"runtime/debug"

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
		_, _ = fmt.Fprintf(w, `{"schema":"wa.version/v1","version":%q}`, v)
		_, _ = fmt.Fprintln(w)
		return
	}
	_, _ = fmt.Fprintf(w, "wa version %s\n", v)
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
