package main

import (
	"encoding/json"
	"fmt"

	"github.com/spf13/cobra"
)

var (
	threadChat   string
	threadCursor string
	threadLimit  int
)

var threadCmd = &cobra.Command{
	Use:   "thread",
	Short: "Thread retrieval operations",
}

var threadGetCmd = &cobra.Command{
	Use:   "get",
	Short: "Fetch a window of messages for a chat JID",
	RunE: func(cmd *cobra.Command, args []string) error {
		if threadChat == "" {
			return exitf(64, "wa thread get: --chat is required")
		}
		params, _ := json.Marshal(map[string]any{
			"chat":   threadChat,
			"cursor": threadCursor,
			"limit":  threadLimit,
		})
		result, exitCode, err := callAndClose(flagSocket, "thread.get", params)
		if err != nil {
			return exiterr(exitCode, err)
		}
		fmt.Println(formatResult("thread.get", result, flagJSON))
		return nil
	},
}

func init() {
	threadGetCmd.Flags().StringVar(&threadChat, "chat", "", "chat JID")
	threadGetCmd.Flags().StringVar(&threadCursor, "cursor", "", "pagination cursor")
	threadGetCmd.Flags().IntVar(&threadLimit, "limit", 50, "page size (≤200)")
	threadCmd.AddCommand(threadGetCmd)
	rootCmd.AddCommand(threadCmd)
}
