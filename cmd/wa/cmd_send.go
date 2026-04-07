package main

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

var (
	sendTo   string
	sendBody string
)

var sendCmd = &cobra.Command{
	Use:   "send",
	Short: "Send a text message",
	RunE: func(cmd *cobra.Command, args []string) error {
		if sendTo == "" || sendBody == "" {
			fmt.Fprintln(os.Stderr, "wa send: --to and --body are required")
			os.Exit(64)
		}

		params := map[string]any{
			"to":   sendTo,
			"body": sendBody,
		}

		result, exitCode, err := callAndClose(flagSocket, "send", params)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(exitCode)
		}

		fmt.Println(formatResult("send", result, flagJSON))
		return nil
	},
}

func init() {
	sendCmd.Flags().StringVar(&sendTo, "to", "", "recipient JID (e.g. 5511999999999@s.whatsapp.net)")
	sendCmd.Flags().StringVar(&sendBody, "body", "", "message text")
}
