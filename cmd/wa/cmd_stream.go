package main

import (
	"strings"

	"github.com/spf13/cobra"
)

var streamChat string

// streamCmd is a convenience wrapper over `wa subscribe --events message`
// for the common "live-tail incoming messages" case (#174). It opens the
// same server-push subscription and prints each inbound message as one
// NDJSON line, until interrupted (ctrl-C).
//
// For receipts, status, or typing events — or to resume from a cursor
// (--since) — use `wa subscribe` directly.
var streamCmd = &cobra.Command{
	Use:   "stream",
	Short: "Live-tail incoming messages as NDJSON (wraps `wa subscribe --events message`)",
	Long: `Stream incoming messages from the daemon as newline-delimited JSON —
one object per message — until interrupted (ctrl-C).

This is a convenience wrapper over:

    wa subscribe --events message [--chats <jid>]

For receipts, status, or typing events, or to resume from a cursor, use
wa subscribe directly.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		params := map[string]any{"events": []string{"message"}}
		if streamChat != "" {
			params["chats"] = strings.Split(streamChat, ",")
		}
		return runSubscribeStream(cmd, params)
	},
}

func init() {
	streamCmd.Flags().StringVar(&streamChat, "chat", "", "only stream messages for this chat JID (comma-separated for multiple)")
	rootCmd.AddCommand(streamCmd)
}
