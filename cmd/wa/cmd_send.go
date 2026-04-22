package main

import (
	"fmt"

	"github.com/spf13/cobra"
)

var (
	sendTo             string
	sendBody           string
	sendIdempotencyKey string
)

var sendCmd = &cobra.Command{
	Use:   "send",
	Short: "Send a text message",
	RunE: func(cmd *cobra.Command, args []string) error {
		if sendTo == "" || sendBody == "" {
			return exitf(64, "wa send: --to and --body are required")
		}

		params := map[string]any{
			"to":   sendTo,
			"body": sendBody,
		}
		if sendIdempotencyKey != "" {
			params["idempotencyKey"] = sendIdempotencyKey
		}

		result, exitCode, err := callAndClose(flagSocket, "send", params)
		if err != nil {
			return exiterr(exitCode, err)
		}

		fmt.Println(formatResult("send", result, flagJSON))
		return nil
	},
}

func init() {
	sendCmd.Flags().StringVar(&sendTo, "to", "", "recipient JID (e.g. 5511999999999@s.whatsapp.net)")
	sendCmd.Flags().StringVar(&sendBody, "body", "", "message text")
	sendCmd.Flags().StringVar(&sendIdempotencyKey, "idempotency-key", "", "FR-034a replay key; same key + params replays cached result, same key + different params returns -32101")
}
