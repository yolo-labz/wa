package main

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

var (
	purgeChat string
	purgeYes  bool
)

var purgeCmd = &cobra.Command{
	Use:   "purge",
	Short: "Delete all messages for a chat",
	RunE: func(cmd *cobra.Command, args []string) error {
		if purgeChat == "" {
			return exitf(64, "wa purge: --chat is required")
		}
		if !purgeYes {
			// Dry run: query history to show count
			params, _ := json.Marshal(map[string]any{"chat": purgeChat, "limit": 1})
			result, exitCode, err := callAndClose(flagSocket, "history", params)
			if err != nil {
				return exiterr(exitCode, err)
			}
			fmt.Fprintf(os.Stderr, "This will delete all messages for %s. Use --yes to confirm.\n", purgeChat)
			_ = result
			return nil
		}
		params, _ := json.Marshal(map[string]any{"chat": purgeChat})
		result, exitCode, err := callAndClose(flagSocket, "purge", params)
		if err != nil {
			return exiterr(exitCode, err)
		}
		if flagJSON {
			fmt.Println(formatJSON("purge", result))
		} else {
			var resp struct {
				Deleted int64 `json:"deleted"`
			}
			_ = json.Unmarshal(result, &resp)
			fmt.Printf("Purged %d messages from %s\n", resp.Deleted, purgeChat)
		}
		return nil
	},
}

func init() {
	purgeCmd.Flags().StringVar(&purgeChat, "chat", "", "chat JID to purge")
	purgeCmd.Flags().BoolVarP(&purgeYes, "yes", "y", false, "confirm deletion (required)")
}
