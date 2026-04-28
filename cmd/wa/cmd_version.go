package main

import (
	"fmt"
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

var versionCmd = &cobra.Command{
	Use:   "version",
	Short: "Print the wa CLI version",
	Run: func(cmd *cobra.Command, args []string) {
		v := resolveVersion()
		if flagJSON {
			fmt.Printf(`{"schema":"wa.version/v1","version":%q}`, v)
			fmt.Println()
		} else {
			fmt.Printf("wa version %s\n", v)
		}
	},
}
