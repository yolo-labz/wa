// Package main is the wa thin CLI client. Every subcommand is a single
// JSON-RPC call against the wad daemon over a unix socket. No use case
// logic lives here.
package main

import (
	"errors"
	"fmt"
	"os"
)

func main() {
	os.Exit(run())
}

func run() int {
	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		var ee *exitError
		if errors.As(err, &ee) {
			return ee.ExitCode()
		}
		return 1
	}
	return 0
}
