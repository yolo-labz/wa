package main

import (
	"encoding/json"

	"github.com/spf13/cobra"
)

var messagesLimit int

var messagesCmd = &cobra.Command{
	Use:   "messages",
	Short: "List recent messages across all chats",
	RunE: func(cmd *cobra.Command, args []string) error {
		params, _ := json.Marshal(map[string]any{"limit": messagesLimit})
		result, exitCode, err := callAndClose(flagSocket, "messages", params)
		if err != nil {
			return exiterr(exitCode, err)
		}
		if flagJSON {
			printNDJSON("wa.messages/v1", result)
			return nil
		}
		printMessageTable(result)
		return nil
	},
}

func init() {
	messagesCmd.Flags().IntVar(&messagesLimit, "limit", 50, "max messages to return")
}
