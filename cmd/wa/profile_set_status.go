package main

import (
	"fmt"

	"github.com/spf13/cobra"
)

var (
	profileSetStatusStatus         string
	profileSetStatusIdempotencyKey string
)

var profileSetStatusCmd = &cobra.Command{
	Use:   "set-status",
	Short: "Set the account's About/status text (FR-027, ≤139 bytes)",
	Long: `Updates the About text via the direct status IQ. The 139-byte cap
mirrors the server-side limit. Writes an AuditProfileEdit entry.`,
	Annotations: map[string]string{"profile": "skip"},
	RunE: func(cmd *cobra.Command, args []string) error {
		if profileSetStatusStatus == "" {
			return exitf(64, "wa profile set-status: --status is required")
		}
		params := map[string]any{"status": profileSetStatusStatus}
		if profileSetStatusIdempotencyKey != "" {
			params["idempotencyKey"] = profileSetStatusIdempotencyKey
		}
		result, exitCode, err := callAndClose(flagSocket, "profile.setStatus", params)
		if err != nil {
			return exiterr(exitCode, err)
		}
		fmt.Println(formatResult("profile.setStatus", result, flagJSON))
		return nil
	},
}

func init() {
	profileSetStatusCmd.Flags().StringVar(&profileSetStatusStatus, "status", "", "new About text (required, ≤139 bytes)")
	profileSetStatusCmd.Flags().StringVar(&profileSetStatusIdempotencyKey, "idempotency-key", "", "FR-034a replay key")
	profileCmd.AddCommand(profileSetStatusCmd)
}
