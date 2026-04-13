package main

import (
	"fmt"

	"github.com/spf13/cobra"
)

var (
	reactChat  string
	reactMsgID string
	reactEmoji string
)

var reactCmd = &cobra.Command{
	Use:   "react",
	Short: "React to a message with an emoji",
	RunE: func(cmd *cobra.Command, args []string) error {
		if reactChat == "" || reactMsgID == "" {
			return exitf(64, "wa react: --chat and --messageId are required")
		}

		params := map[string]any{
			"chat":      reactChat,
			"messageId": reactMsgID,
			"emoji":     reactEmoji,
		}

		result, exitCode, err := callAndClose(flagSocket, "react", params)
		if err != nil {
			return exiterr(exitCode, err)
		}

		fmt.Println(formatResult("react", result, flagJSON))
		return nil
	},
}

func init() {
	reactCmd.Flags().StringVar(&reactChat, "chat", "", "chat JID")
	reactCmd.Flags().StringVar(&reactMsgID, "messageId", "", "message ID to react to")
	reactCmd.Flags().StringVar(&reactEmoji, "emoji", "", "emoji to send (empty removes reaction)")
}
