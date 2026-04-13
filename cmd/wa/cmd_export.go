package main

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

var exportChat string

var exportCmd = &cobra.Command{
	Use:   "export",
	Short: "Export all messages for a chat as NDJSON",
	RunE: func(cmd *cobra.Command, args []string) error {
		if exportChat == "" {
			fmt.Fprintln(os.Stderr, "wa export: --chat is required")
			os.Exit(64)
		}
		params, _ := json.Marshal(map[string]any{"chat": exportChat})
		result, exitCode, err := callAndClose(flagSocket, "export", params)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(exitCode)
		}
		printNDJSON("wa.export/v1", result)
		return nil
	},
}

func init() {
	exportCmd.Flags().StringVar(&exportChat, "chat", "", "chat JID to export")
}
