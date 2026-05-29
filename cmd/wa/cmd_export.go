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
		// A zero-row export is indistinguishable from a typo'd / nonexistent
		// JID in the current schema (there is no chats table — only the
		// messages table, so "chat exists but empty" and "chat never seen"
		// both yield 0 rows). Rather than the old silent exit-0 that made
		// every JID-guess miss look like success (#177), exit 64 with a
		// stderr message so callers brute-forcing JID variants can tell a
		// miss from a hit. stdout stays clean for the pipe; main() prints
		// the returned error to stderr.
		n := printNDJSON("wa.export/v1", result)
		if n == 0 {
			return exitf(64, "wa export: 0 messages for %q — chat is empty or not "+
				"in the local store; re-check the JID (try `wa contacts search` or "+
				"`wa chat list`)", exportChat)
		}
		return nil
	},
}

func init() {
	exportCmd.Flags().StringVar(&exportChat, "chat", "", "chat JID to export")
}
