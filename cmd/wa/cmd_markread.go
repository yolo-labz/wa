package main

import (
	"fmt"
	"os"

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
			fmt.Fprintln(os.Stderr, "wa markRead: --chat and --messageId are required")
			os.Exit(64)
		}

		params := map[string]any{
			"chat":      markReadChat,
			"messageId": markReadMsgID,
		}

		result, exitCode, err := callAndClose(flagSocket, "markRead", params)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(exitCode)
		}

		fmt.Println(formatResult("markRead", result, flagJSON))
		return nil
	},
}

func init() {
	markReadCmd.Flags().StringVar(&markReadChat, "chat", "", "chat JID")
	markReadCmd.Flags().StringVar(&markReadMsgID, "messageId", "", "message ID to mark as read")
}
