package main

import (
	"encoding/json"
	"fmt"

	"github.com/spf13/cobra"
)

var (
	mediaSHA256     string
	mediaMessageID  string
	mediaTranscribe bool
	mediaOlderSecs  int64
	mediaDryRun     bool
)

var mediaCmd = &cobra.Command{
	Use:   "media",
	Short: "Content-addressed media operations",
}

var mediaResolveCmd = &cobra.Command{
	Use:   "resolve",
	Short: "Return the on-disk path for a content-addressed sha256",
	RunE: func(cmd *cobra.Command, args []string) error {
		if mediaSHA256 == "" {
			return exitf(64, "wa media resolve: --sha256 is required")
		}
		params, _ := json.Marshal(map[string]any{"sha256": mediaSHA256})
		result, exitCode, err := callAndClose(flagSocket, "media.resolve", params)
		if err != nil {
			return exiterr(exitCode, err)
		}
		fmt.Println(formatResult("media.resolve", result, flagJSON))
		return nil
	},
}

var mediaDownloadCmd = &cobra.Command{
	Use:   "download",
	Short: "Lazy-fetch the media payload for a messageId; prints the on-disk path",
	RunE: func(cmd *cobra.Command, args []string) error {
		if mediaMessageID == "" {
			return exitf(64, "wa media download: --message-id is required")
		}
		params, _ := json.Marshal(map[string]any{
			"messageId":  mediaMessageID,
			"transcribe": mediaTranscribe,
		})
		result, exitCode, err := callAndClose(flagSocket, "media.download", params)
		if err != nil {
			return exiterr(exitCode, err)
		}
		if flagJSON {
			fmt.Println(formatResult("media.download", result, true))
			return nil
		}
		// Print the on-disk path on stdout (FR-050a contract).
		var obj struct {
			Object struct {
				Path string `json:"path"`
			} `json:"object"`
		}
		_ = json.Unmarshal(result, &obj)
		fmt.Println(obj.Object.Path)
		return nil
	},
}

var mediaGCCmd = &cobra.Command{
	Use:   "gc",
	Short: "Garbage-collect media older than a given window",
	RunE: func(cmd *cobra.Command, args []string) error {
		params, _ := json.Marshal(map[string]any{
			"olderThanSeconds": mediaOlderSecs,
			"dryRun":           mediaDryRun,
		})
		result, exitCode, err := callAndClose(flagSocket, "media.gc", params)
		if err != nil {
			return exiterr(exitCode, err)
		}
		fmt.Println(formatResult("media.gc", result, flagJSON))
		return nil
	},
}

func init() {
	mediaResolveCmd.Flags().StringVar(&mediaSHA256, "sha256", "", "64-hex content hash")
	mediaDownloadCmd.Flags().StringVar(&mediaMessageID, "message-id", "", "originating message id")
	mediaDownloadCmd.Flags().BoolVar(&mediaTranscribe, "transcribe", false, "transcribe voice notes")
	mediaGCCmd.Flags().Int64Var(&mediaOlderSecs, "older-than-seconds", 30*86400, "cutoff age")
	mediaGCCmd.Flags().BoolVar(&mediaDryRun, "dry-run", false, "candidate count without deletion")

	mediaCmd.AddCommand(mediaResolveCmd)
	mediaCmd.AddCommand(mediaDownloadCmd)
	mediaCmd.AddCommand(mediaGCCmd)
	rootCmd.AddCommand(mediaCmd)
}
