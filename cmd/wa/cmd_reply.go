package main

import (
	"fmt"

	"github.com/spf13/cobra"
)

var (
	replyTo             string
	replyQuotedID       string
	replyBody           string
	replyIdempotencyKey string
)

var replyCmd = &cobra.Command{
	Use:   "reply",
	Short: "Send a quoted reply (FR-070)",
	RunE: func(cmd *cobra.Command, args []string) error {
		if replyTo == "" || replyQuotedID == "" || replyBody == "" {
			return exitf(64, "wa reply: --to, --quoted-id and --body are required")
		}
		params := map[string]any{
			"to":       replyTo,
			"quotedId": replyQuotedID,
			"body":     replyBody,
		}
		if replyIdempotencyKey != "" {
			params["idempotencyKey"] = replyIdempotencyKey
		}
		result, exitCode, err := callAndClose(flagSocket, "send.reply", params)
		if err != nil {
			return exiterr(exitCode, err)
		}
		fmt.Println(formatResult("send.reply", result, flagJSON))
		return nil
	},
}

func init() {
	replyCmd.Flags().StringVar(&replyTo, "to", "", "recipient JID")
	replyCmd.Flags().StringVar(&replyQuotedID, "quoted-id", "", "message ID being quoted")
	replyCmd.Flags().StringVar(&replyBody, "body", "", "reply text")
	replyCmd.Flags().StringVar(&replyIdempotencyKey, "idempotency-key", "", "FR-034a replay key; same key + params replays cached result, same key + different params returns -32101")
}
