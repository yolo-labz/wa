package main

import (
	"fmt"

	"github.com/spf13/cobra"
)

// sessionCmd is the `wa session` parent for FR-031 logout-all.
var sessionCmd = &cobra.Command{
	Use:   "session",
	Short: "Manage the WhatsApp account session (logout-all)",
}

var sessionLogoutAllIdempotencyKey string

var sessionLogoutAllCmd = &cobra.Command{
	Use:   "logout-all",
	Short: "Unlink every device from the WhatsApp account (FR-031)",
	Long: `logout-all unlinks every paired device from the WhatsApp account,
not just this client. The next pair flow on any device starts fresh.

At the current whatsmeow commit pin this returns -32000 upstream_error
because the upstream library has not yet shipped a LogoutAll helper.
When the helper lands the command will complete successfully and write
an AuditLogoutAll entry.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		params := map[string]any{}
		if sessionLogoutAllIdempotencyKey != "" {
			params["idempotencyKey"] = sessionLogoutAllIdempotencyKey
		}
		result, exitCode, err := callAndClose(flagSocket, "session.logoutAll", params)
		if err != nil {
			return exiterr(exitCode, err)
		}
		fmt.Println(formatResult("session.logoutAll", result, flagJSON))
		return nil
	},
}

func init() {
	sessionLogoutAllCmd.Flags().StringVar(&sessionLogoutAllIdempotencyKey, "idempotency-key", "", "FR-034a replay key")
	sessionCmd.AddCommand(sessionLogoutAllCmd)
	rootCmd.AddCommand(sessionCmd)
}
