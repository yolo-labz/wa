package main

import (
	"encoding/json"

	"github.com/spf13/cobra"
)

var exportChat string

var exportCmd = &cobra.Command{
	Use:   "export",
	Short: "Export all messages for a chat as NDJSON",
	RunE: func(cmd *cobra.Command, args []string) error {
		if exportChat == "" {
			return exitf(64, "wa export: --chat is required")
		}
		params, _ := json.Marshal(map[string]any{"chat": exportChat})
		result, exitCode, err := callAndClose(flagSocket, "export", params)
		if err != nil {
			return exiterr(exitCode, err)
		}
		printNDJSON("wa.export/v1", result)
		return nil
	},
}

func init() {
	exportCmd.Flags().StringVar(&exportChat, "chat", "", "chat JID to export")
}
