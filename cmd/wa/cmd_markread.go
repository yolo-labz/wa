package main

import (
	"fmt"

	"github.com/spf13/cobra"
)

var (
	markReadChat  string
	markReadMsgID string
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
}
