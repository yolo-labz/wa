package main

import (
	"fmt"

	"github.com/spf13/cobra"
)

var (
	sendSeenChat           string
	sendSeenIdempotencyKey string
)

var sendSeenCmd = &cobra.Command{
	Use:   "sendSeen",
	Short: "Mark a chat read at its newest incoming message",
	Long: `Mark a chat read ("seen") without knowing a message id: the daemon
looks up the newest incoming message in its local history and anchors
the read receipt there. Use markRead instead when you need to anchor an
explicit message id. Fails with -32117 when the daemon has no incoming
message on record for the chat.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		if sendSeenChat == "" {
			return exitf(64, "wa sendSeen: --chat is required")
		}

		params := map[string]any{"chat": sendSeenChat}
		if sendSeenIdempotencyKey != "" {
			params["idempotencyKey"] = sendSeenIdempotencyKey
		}

		result, exitCode, err := callAndClose(flagSocket, "sendSeen", params)
		if err != nil {
			return exiterr(exitCode, err)
		}

		fmt.Println(formatResult("sendSeen", result, flagJSON))
		return nil
	},
}

func init() {
	sendSeenCmd.Flags().StringVar(&sendSeenChat, "chat", "", "chat JID")
	sendSeenCmd.Flags().StringVar(&sendSeenIdempotencyKey, "idempotency-key", "", "FR-034a replay key; same key + params replays cached result, same key + different params returns -32101")
}
