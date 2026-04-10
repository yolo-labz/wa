package main

import (
	"fmt"
	"os"

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
			fmt.Fprintln(os.Stderr, "wa react: --chat and --messageId are required")
			os.Exit(64)
		}

		params := map[string]any{
			"chat":      reactChat,
			"messageId": reactMsgID,
			"emoji":     reactEmoji,
		}

		result, exitCode, err := callAndClose(flagSocket, "react", params)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(exitCode)
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
