package main

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

var (
	sendMediaTo      string
	sendMediaPath    string
	sendMediaCaption string
	sendMediaMime    string
)

var sendMediaCmd = &cobra.Command{
	Use:   "sendMedia",
	Short: "Send a media message (image, video, document, etc.)",
	RunE: func(cmd *cobra.Command, args []string) error {
		if sendMediaTo == "" || sendMediaPath == "" {
			fmt.Fprintln(os.Stderr, "wa sendMedia: --to and --path are required")
			os.Exit(64)
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

		result, exitCode, err := callAndClose(flagSocket, "sendMedia", params)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(exitCode)
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
}
