package main

import (
	"fmt"

	"github.com/spf13/cobra"
)

var (
	sendMediaTo             string
	sendMediaPath           string
	sendMediaCaption        string
	sendMediaMime           string
	sendMediaIdempotencyKey string
)

var sendMediaCmd = &cobra.Command{
	Use:   "sendMedia",
	Short: "Send a media message (image, video, document, etc.)",
	RunE: func(cmd *cobra.Command, args []string) error {
		if sendMediaTo == "" || sendMediaPath == "" {
			return exitf(64, "wa sendMedia: --to and --path are required")
		}

		params := map[string]any{
			"to":   sendMediaTo,
			"path": sendMediaPath,
		}
		if sendMediaCaption != "" {
			params["caption"] = sendMediaCaption
		}
		if sendMediaMime != "" {
			params["mime"] = sendMediaMime
		}
		if sendMediaIdempotencyKey != "" {
			params["idempotencyKey"] = sendMediaIdempotencyKey
		}

		result, exitCode, err := callAndClose(flagSocket, "sendMedia", params)
		if err != nil {
			return exiterr(exitCode, err)
		}

		fmt.Println(formatResult("sendMedia", result, flagJSON))
		return nil
	},
}

func init() {
	sendMediaCmd.Flags().StringVar(&sendMediaTo, "to", "", "recipient JID")
	sendMediaCmd.Flags().StringVar(&sendMediaPath, "path", "", "path to media file on daemon's filesystem")
	sendMediaCmd.Flags().StringVar(&sendMediaCaption, "caption", "", "optional caption")
	sendMediaCmd.Flags().StringVar(&sendMediaMime, "mime", "", "optional MIME type override")
	sendMediaCmd.Flags().StringVar(&sendMediaIdempotencyKey, "idempotency-key", "", "FR-034a replay key; same key + params replays cached result, same key + different params returns -32101")
	// #194: accept --chat as a universal recipient alias for --to.
	applyChatAlias(sendMediaCmd, "to")
}
