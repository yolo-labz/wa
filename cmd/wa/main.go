// Package main is the wa thin CLI client. Every subcommand is a single
// JSON-RPC call against the wad daemon over a unix socket. No use case
// logic lives here.
package main

import (
	"fmt"
	"os"
)

func main() {
	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
