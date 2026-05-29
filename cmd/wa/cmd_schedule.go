package main

import (
	"encoding/json"
	"fmt"
	"strconv"
	"time"

	"github.com/spf13/cobra"
)

var (
	scheduleTo             string
	scheduleBody           string
	scheduleFireAt         string
	scheduleKind           string
	scheduleMediaPath      string
	scheduleID             string
	scheduleState          string
	scheduleLimit          int
	scheduleIdempotencyKey string
)

var scheduleCmd = &cobra.Command{
	Use:   "schedule",
	Short: "Schedule future WhatsApp sends (pending → fired|cancelled|failed)",
}

var scheduleSendCmd = &cobra.Command{
	Use:   "send",
	Short: "Schedule a message for future delivery",
	RunE: func(cmd *cobra.Command, args []string) error {
		if scheduleTo == "" || scheduleFireAt == "" {
			return exitf(64, "wa schedule send: --to and --fire-at are required")
		}
		fireAt, err := parseFireAt(scheduleFireAt)
		if err != nil {
			return exitf(64, "wa schedule send: %v", err)
		}
		params := map[string]any{
			"to":     scheduleTo,
			"fireAt": fireAt,
		}
		if scheduleBody != "" {
			params["body"] = scheduleBody
		}
		if scheduleMediaPath != "" {
			params["mediaPath"] = scheduleMediaPath
		}
		if scheduleKind != "" {
			params["kind"] = scheduleKind
		}
		if scheduleID != "" {
			params["id"] = scheduleID
		}
		if scheduleIdempotencyKey != "" {
			params["idempotencyKey"] = scheduleIdempotencyKey
		}
		raw, _ := json.Marshal(params)
		result, exitCode, err := callAndClose(flagSocket, "schedule.send", raw)
		if err != nil {
			return exiterr(exitCode, err)
		}
		fmt.Println(formatResult("schedule.send", result, flagJSON))
		return nil
	},
}

var scheduleListCmd = &cobra.Command{
	Use:   "list",
	Short: "List scheduled sends (default: pending)",
	RunE: func(cmd *cobra.Command, args []string) error {
		params := map[string]any{"state": scheduleState, "limit": scheduleLimit}
		raw, _ := json.Marshal(params)
		result, exitCode, err := callAndClose(flagSocket, "schedule.list", raw)
		if err != nil {
			return exiterr(exitCode, err)
		}
		fmt.Println(formatResult("schedule.list", result, flagJSON))
		return nil
	},
}

var scheduleCancelCmd = &cobra.Command{
	Use:   "cancel",
	Short: "Cancel a pending scheduled send",
	RunE: func(cmd *cobra.Command, args []string) error {
		if scheduleID == "" {
			return exitf(64, "wa schedule cancel: --id is required")
		}
		raw, _ := json.Marshal(map[string]any{"id": scheduleID})
		result, exitCode, err := callAndClose(flagSocket, "schedule.cancel", raw)
		if err != nil {
			return exiterr(exitCode, err)
		}
		fmt.Println(formatResult("schedule.cancel", result, flagJSON))
		return nil
	},
}

var scheduleUpdateCmd = &cobra.Command{
	Use:   "update",
	Short: "Reschedule a pending send to a new fire_at",
	RunE: func(cmd *cobra.Command, args []string) error {
		if scheduleID == "" || scheduleFireAt == "" {
			return exitf(64, "wa schedule update: --id and --fire-at are required")
		}
		fireAt, err := parseFireAt(scheduleFireAt)
		if err != nil {
			return exitf(64, "wa schedule update: %v", err)
		}
		raw, _ := json.Marshal(map[string]any{"id": scheduleID, "fireAt": fireAt})
		result, exitCode, err := callAndClose(flagSocket, "schedule.update", raw)
		if err != nil {
			return exiterr(exitCode, err)
		}
		fmt.Println(formatResult("schedule.update", result, flagJSON))
		return nil
	},
}

// parseFireAt accepts either a unix timestamp in seconds OR an RFC3339
// datetime string, returning the unix-seconds form expected by the daemon.
func parseFireAt(s string) (int64, error) {
	if n, err := strconv.ParseInt(s, 10, 64); err == nil {
		return n, nil
	}
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return 0, fmt.Errorf("--fire-at must be unix-seconds or RFC3339: %w", err)
	}
	return t.Unix(), nil
}

func init() {
	scheduleSendCmd.Flags().StringVar(&scheduleTo, "to", "", "recipient JID")
	scheduleSendCmd.Flags().StringVar(&scheduleBody, "body", "", "message body (text or caption)")
	scheduleSendCmd.Flags().StringVar(&scheduleMediaPath, "media", "", "media file path (daemon-side)")
	scheduleSendCmd.Flags().StringVar(&scheduleKind, "kind", "", "send_text (default) | send_media | create_draft")
	scheduleSendCmd.Flags().StringVar(&scheduleFireAt, "fire-at", "", "fire time: unix-seconds or RFC3339")
	scheduleSendCmd.Flags().StringVar(&scheduleID, "id", "", "optional explicit schedule id")
	scheduleSendCmd.Flags().StringVar(&scheduleIdempotencyKey, "idempotency-key", "", "FR-034a replay key; same key + params replays cached result, same key + different params returns -32101")
	// #194: accept --chat as a universal recipient alias for --to.
	applyChatAlias(scheduleSendCmd, "to")

	scheduleListCmd.Flags().StringVar(&scheduleState, "state", "pending", "pending | fired | cancelled | failed | '' (all)")
	scheduleListCmd.Flags().IntVar(&scheduleLimit, "limit", 50, "max results (<=200)")

	scheduleCancelCmd.Flags().StringVar(&scheduleID, "id", "", "schedule id")

	scheduleUpdateCmd.Flags().StringVar(&scheduleID, "id", "", "schedule id")
	scheduleUpdateCmd.Flags().StringVar(&scheduleFireAt, "fire-at", "", "new fire time: unix-seconds or RFC3339")

	scheduleCmd.AddCommand(scheduleSendCmd)
	scheduleCmd.AddCommand(scheduleListCmd)
	scheduleCmd.AddCommand(scheduleCancelCmd)
	scheduleCmd.AddCommand(scheduleUpdateCmd)
	rootCmd.AddCommand(scheduleCmd)
}
