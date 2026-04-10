package main

import (
	"github.com/spf13/cobra"

	"github.com/yolo-labz/wa/internal/adapters/primary/socket"
)

// Global flags set by persistent flags on the root command.
var (
	flagSocket  string
	flagJSON    bool
	flagVerbose bool
)

// rootCmd is the wa CLI root command.
var rootCmd = &cobra.Command{
	Use:   "wa",
	Short: "WhatsApp automation CLI",
	Long:  "wa is a thin JSON-RPC client for the wad daemon.",
	// Silence cobra's default usage/error printing so we control output.
	SilenceUsage:  true,
	SilenceErrors: true,
}

func init() {
	// Resolve default socket path (best-effort; error surfaced at dial time).
	defaultSocket := ""
	if p, err := socket.Path(); err == nil {
		defaultSocket = p
	}

	rootCmd.PersistentFlags().StringVar(&flagSocket, "socket", defaultSocket, "path to wad unix socket")
	rootCmd.PersistentFlags().BoolVar(&flagJSON, "json", false, "output NDJSON instead of human-readable text")
	rootCmd.PersistentFlags().BoolVar(&flagVerbose, "verbose", false, "enable verbose output")

	// Register subcommands.
	rootCmd.AddCommand(pairCmd)
	rootCmd.AddCommand(sendCmd)
	rootCmd.AddCommand(statusCmd)
	rootCmd.AddCommand(allowCmd)
	rootCmd.AddCommand(panicCmd)
}
