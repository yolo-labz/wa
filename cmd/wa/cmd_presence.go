package main

import (
	"fmt"

	"github.com/spf13/cobra"
)

// presenceCmd is the `wa presence` parent for typing/recording indicators
// (FR-032). Non-idempotent by spec — excess calls are silently rate-
// limited by the adapter (1/s/chat), no idempotency key is accepted or
// forwarded. Four subcommands mirror the wire method table:
//
//	wa presence composing start --chat <jid>
//	wa presence composing stop  --chat <jid>
//	wa presence recording start --chat <jid>
//	wa presence recording stop  --chat <jid>
var presenceCmd = &cobra.Command{
	Use:   "presence",
	Short: "Send typing / recording indicators (FR-032)",
}

var (
	presenceComposingChat string
	presenceRecordingChat string
)

var presenceComposingCmd = &cobra.Command{
	Use:   "composing",
	Short: "Composing (typing) presence start/stop",
}

var presenceComposingStartCmd = &cobra.Command{
	Use:   "start",
	Short: "Start composing indicator (typing...)",
	RunE: func(cmd *cobra.Command, args []string) error {
		return runPresence("presence.composing.start", presenceComposingChat)
	},
}

var presenceComposingStopCmd = &cobra.Command{
	Use:   "stop",
	Short: "Stop composing indicator",
	RunE: func(cmd *cobra.Command, args []string) error {
		return runPresence("presence.composing.stop", presenceComposingChat)
	},
}

var presenceRecordingCmd = &cobra.Command{
	Use:   "recording",
	Short: "Recording (voice note) presence start/stop",
}

var presenceRecordingStartCmd = &cobra.Command{
	Use:   "start",
	Short: "Start recording indicator",
	RunE: func(cmd *cobra.Command, args []string) error {
		return runPresence("presence.recording.start", presenceRecordingChat)
	},
}

var presenceRecordingStopCmd = &cobra.Command{
	Use:   "stop",
	Short: "Stop recording indicator",
	RunE: func(cmd *cobra.Command, args []string) error {
		return runPresence("presence.recording.stop", presenceRecordingChat)
	},
}

func runPresence(method, chat string) error {
	if chat == "" {
		return exitf(64, "wa %s: --chat is required", method)
	}
	result, exitCode, err := callAndClose(flagSocket, method, map[string]any{"chat": chat})
	if err != nil {
		return exiterr(exitCode, err)
	}
	fmt.Println(formatResult(method, result, flagJSON))
	return nil
}

func init() {
	presenceComposingStartCmd.Flags().StringVar(&presenceComposingChat, "chat", "", "chat JID")
	presenceComposingStopCmd.Flags().StringVar(&presenceComposingChat, "chat", "", "chat JID")
	presenceRecordingStartCmd.Flags().StringVar(&presenceRecordingChat, "chat", "", "chat JID")
	presenceRecordingStopCmd.Flags().StringVar(&presenceRecordingChat, "chat", "", "chat JID")

	presenceComposingCmd.AddCommand(presenceComposingStartCmd, presenceComposingStopCmd)
	presenceRecordingCmd.AddCommand(presenceRecordingStartCmd, presenceRecordingStopCmd)
	presenceCmd.AddCommand(presenceComposingCmd, presenceRecordingCmd)
	rootCmd.AddCommand(presenceCmd)
}
