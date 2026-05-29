package main

import (
	"fmt"
	"strconv"

	"github.com/spf13/cobra"
)

var (
	msgForwardTo             string
	msgForwardSourceChat     string
	msgForwardID             string
	msgForwardIdempotencyKey string
)

var msgForwardCmd = &cobra.Command{
	Use:   "forward",
	Short: "Forward a received message to another chat (FR-033)",
	Long:  "Forward re-sends an existing message to --to with the \"Forwarded\" chip. Rate-limit + allowlist apply to the destination chat, not the source.",
	RunE: func(cmd *cobra.Command, args []string) error {
		if msgForwardTo == "" || msgForwardSourceChat == "" || msgForwardID == "" {
			return exitf(64, "wa msg forward: --to, --sourceChat, and --messageId are required")
		}
		params := map[string]any{
			"to":         msgForwardTo,
			"sourceChat": msgForwardSourceChat,
			"messageId":  msgForwardID,
		}
		if msgForwardIdempotencyKey != "" {
			params["idempotencyKey"] = msgForwardIdempotencyKey
		}
		result, exitCode, err := callAndClose(flagSocket, "message.forward", params)
		if err != nil {
			return exiterr(exitCode, err)
		}
		fmt.Println(formatResult("message.forward", result, flagJSON))
		return nil
	},
}

var (
	msgStarChat           string
	msgStarID             string
	msgStarUnstar         bool
	msgStarIdempotencyKey string
)

var msgStarCmd = &cobra.Command{
	Use:   "star",
	Short: "Star (or --unstar) a message (FR-033)",
	Long:  "Star marks a message in the starred folder. Default is star=true; pass --unstar to remove the mark. Idempotent: repeating the same state is a no-op.",
	RunE: func(cmd *cobra.Command, args []string) error {
		if msgStarChat == "" || msgStarID == "" {
			return exitf(64, "wa msg star: --chat and --messageId are required")
		}
		params := map[string]any{
			"chat":      msgStarChat,
			"messageId": msgStarID,
			"starred":   !msgStarUnstar,
		}
		if msgStarIdempotencyKey != "" {
			params["idempotencyKey"] = msgStarIdempotencyKey
		}
		result, exitCode, err := callAndClose(flagSocket, "message.star", params)
		if err != nil {
			return exiterr(exitCode, err)
		}
		fmt.Println(formatResult("message.star", result, flagJSON))
		return nil
	},
}

var (
	msgDisappearingChat           string
	msgDisappearingSeconds        string
	msgDisappearingIdempotencyKey string
)

// disappearingPresets maps the ergonomic shorthand to the legal enum values.
// Any direct int input that does not match {0, 86400, 604800, 7776000} is
// rejected by the daemon with -32602.
var disappearingPresets = map[string]int{
	"off": 0,
	"24h": 86_400,
	"7d":  604_800,
	"90d": 7_776_000,
}

var msgDisappearingCmd = &cobra.Command{
	Use:   "disappearing",
	Short: "Set the chat-level disappearing-message timer (FR-033, FR-016)",
	Long:  "Disappearing timer enforces the 4-value WhatsApp enum: off|24h|7d|90d (or the equivalent seconds 0, 86400, 604800, 7776000).",
	RunE: func(cmd *cobra.Command, args []string) error {
		if msgDisappearingChat == "" || msgDisappearingSeconds == "" {
			return exitf(64, "wa msg disappearing: --chat and --seconds are required")
		}
		var seconds int
		if v, ok := disappearingPresets[msgDisappearingSeconds]; ok {
			seconds = v
		} else {
			n, err := strconv.Atoi(msgDisappearingSeconds)
			if err != nil {
				return exitf(64, "wa msg disappearing: --seconds must be one of off|24h|7d|90d or a raw integer")
			}
			seconds = n
		}
		params := map[string]any{
			"chat":    msgDisappearingChat,
			"seconds": seconds,
		}
		if msgDisappearingIdempotencyKey != "" {
			params["idempotencyKey"] = msgDisappearingIdempotencyKey
		}
		result, exitCode, err := callAndClose(flagSocket, "message.setDisappearing", params)
		if err != nil {
			return exiterr(exitCode, err)
		}
		fmt.Println(formatResult("message.setDisappearing", result, flagJSON))
		return nil
	},
}

func init() {
	msgForwardCmd.Flags().StringVar(&msgForwardTo, "to", "", "destination chat JID")
	msgForwardCmd.Flags().StringVar(&msgForwardSourceChat, "sourceChat", "", "source chat JID")
	msgForwardCmd.Flags().StringVar(&msgForwardID, "messageId", "", "message ID to forward")
	msgForwardCmd.Flags().StringVar(&msgForwardIdempotencyKey, "idempotency-key", "", "FR-034a replay key; same key + params replays cached result, same key + different params returns -32101")
	// #194: accept --chat as a universal recipient alias for the --to
	// destination. --sourceChat is a distinct name and is left untouched.
	applyChatAlias(msgForwardCmd, "to")

	msgStarCmd.Flags().StringVar(&msgStarChat, "chat", "", "chat JID")
	msgStarCmd.Flags().StringVar(&msgStarID, "messageId", "", "message ID to star/unstar")
	msgStarCmd.Flags().BoolVar(&msgStarUnstar, "unstar", false, "unstar instead of star")
	msgStarCmd.Flags().StringVar(&msgStarIdempotencyKey, "idempotency-key", "", "FR-034a replay key; same key + params replays cached result, same key + different params returns -32101")

	msgDisappearingCmd.Flags().StringVar(&msgDisappearingChat, "chat", "", "chat JID")
	msgDisappearingCmd.Flags().StringVar(&msgDisappearingSeconds, "seconds", "", "timer: off|24h|7d|90d or raw seconds")
	msgDisappearingCmd.Flags().StringVar(&msgDisappearingIdempotencyKey, "idempotency-key", "", "FR-034a replay key; same key + params replays cached result, same key + different params returns -32101")

	msgCmd.AddCommand(msgForwardCmd, msgStarCmd, msgDisappearingCmd)
}
