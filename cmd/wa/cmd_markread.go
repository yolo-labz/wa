package main

import (
	"fmt"

	"github.com/spf13/cobra"
)

var (
	markReadChat           string
	markReadMsgID          string
	markReadIdempotencyKey string
)

var markReadCmd = &cobra.Command{
	Use:   "markRead",
	Short: "Mark a message as read",
	RunE: func(cmd *cobra.Command, args []string) error {
		if markReadChat == "" || markReadMsgID == "" {
			return exitf(64, "wa markRead: --chat and --messageId are required")
		}

		params := map[string]any{
			"chat":      markReadChat,
			"messageId": markReadMsgID,
		}
		if markReadIdempotencyKey != "" {
			params["idempotencyKey"] = markReadIdempotencyKey
		}

		result, exitCode, err := callAndClose(flagSocket, "markRead", params)
		if err != nil {
			return exiterr(exitCode, err)
		}

		fmt.Println(formatResult("markRead", result, flagJSON))
		return nil
	},
}

func init() {
	markReadCmd.Flags().StringVar(&markReadChat, "chat", "", "chat JID")
	markReadCmd.Flags().StringVar(&markReadMsgID, "messageId", "", "message ID to mark as read")
	markReadCmd.Flags().StringVar(&markReadIdempotencyKey, "idempotency-key", "", "FR-034a replay key; same key + params replays cached result, same key + different params returns -32101")
}
