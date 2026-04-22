package main

import (
	"fmt"

	"github.com/spf13/cobra"
)

var (
	profileSetNameName           string
	profileSetNameIdempotencyKey string
)

var profileSetNameCmd = &cobra.Command{
	Use:   "set-name",
	Short: "Set the account's display name (FR-026, ≤25 bytes)",
	Long: `Updates the pushName appstate setting. The 25-byte cap mirrors the
server-side limit — oversized names are refused before hitting the wire.
Writes an AuditProfileEdit entry.`,
	Annotations: map[string]string{"profile": "skip"},
	RunE: func(cmd *cobra.Command, args []string) error {
		if profileSetNameName == "" {
			return exitf(64, "wa profile set-name: --name is required")
		}
		params := map[string]any{"name": profileSetNameName}
		if profileSetNameIdempotencyKey != "" {
			params["idempotencyKey"] = profileSetNameIdempotencyKey
		}
		result, exitCode, err := callAndClose(flagSocket, "profile.setName", params)
		if err != nil {
			return exiterr(exitCode, err)
		}
		fmt.Println(formatResult("profile.setName", result, flagJSON))
		return nil
	},
}

func init() {
	profileSetNameCmd.Flags().StringVar(&profileSetNameName, "name", "", "new display name (required, ≤25 bytes)")
	profileSetNameCmd.Flags().StringVar(&profileSetNameIdempotencyKey, "idempotency-key", "", "FR-034a replay key")
	profileCmd.AddCommand(profileSetNameCmd)
}
