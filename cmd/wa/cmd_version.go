package main

import (
	"fmt"

	"github.com/spf13/cobra"
)

// version is set by ldflags at build time:
//
//	go build -ldflags "-X main.version=v0.1.0" ./cmd/wa
var version = "dev"

var versionCmd = &cobra.Command{
	Use:   "version",
	Short: "Print the wa CLI version",
	Run: func(cmd *cobra.Command, args []string) {
		if flagJSON {
			fmt.Printf(`{"schema":"wa.version/v1","version":%q}`, version)
			fmt.Println()
		} else {
			fmt.Printf("wa version %s\n", version)
		}
	},
}
