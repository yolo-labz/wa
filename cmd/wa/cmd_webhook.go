package main

import (
	"fmt"

	"github.com/spf13/cobra"
)

// Feature 112 — standard-webhooks management CLI. Thin wrappers over
// the webhook.* JSON-RPC methods; the daemon owns delivery state.

var webhookCmd = &cobra.Command{
	Use:   "webhook",
	Short: "Manage outbound webhook endpoints (standard-webhooks signed)",
}

var (
	flagWebhookTopics string
	flagWebhookState  string
	flagWebhookLimit  int
)

var webhookAddCmd = &cobra.Command{
	Use:   "add <url>",
	Short: "Register an endpoint; prints the signing secret ONCE",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		result, exitCode, err := callAndClose(flagSocket, "webhook.add", map[string]any{
			"url": args[0], "topics": flagWebhookTopics,
		})
		if err != nil {
			return exiterr(exitCode, err)
		}
		fmt.Println(formatResult("webhook.add", result, flagJSON))
		return nil
	},
}

var webhookListCmd = &cobra.Command{
	Use:   "list",
	Short: "List registered endpoints (secrets never shown)",
	RunE: func(cmd *cobra.Command, args []string) error {
		result, exitCode, err := callAndClose(flagSocket, "webhook.list", nil)
		if err != nil {
			return exiterr(exitCode, err)
		}
		fmt.Println(formatResult("webhook.list", result, flagJSON))
		return nil
	},
}

var webhookRmCmd = &cobra.Command{
	Use:   "rm <endpoint-id>",
	Short: "Remove an endpoint and its delivery history",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		result, exitCode, err := callAndClose(flagSocket, "webhook.remove", map[string]any{"id": args[0]})
		if err != nil {
			return exiterr(exitCode, err)
		}
		fmt.Println(formatResult("webhook.remove", result, flagJSON))
		return nil
	},
}

var webhookDeliveriesCmd = &cobra.Command{
	Use:   "deliveries",
	Short: "Inspect the delivery queue (pending/delivered/dead)",
	RunE: func(cmd *cobra.Command, args []string) error {
		result, exitCode, err := callAndClose(flagSocket, "webhook.deliveries", map[string]any{
			"state": flagWebhookState, "limit": flagWebhookLimit,
		})
		if err != nil {
			return exiterr(exitCode, err)
		}
		fmt.Println(formatResult("webhook.deliveries", result, flagJSON))
		return nil
	},
}

var webhookReplayCmd = &cobra.Command{
	Use:   "replay <delivery-id>",
	Short: "Re-arm one delivery (including dead-lettered) for an immediate attempt",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		result, exitCode, err := callAndClose(flagSocket, "webhook.replay", map[string]any{"id": args[0]})
		if err != nil {
			return exiterr(exitCode, err)
		}
		fmt.Println(formatResult("webhook.replay", result, flagJSON))
		return nil
	},
}

func init() {
	webhookAddCmd.Flags().StringVar(&flagWebhookTopics, "topics", "message",
		`comma-separated event topics (e.g. "message,receipt", "*" for all)`)
	webhookDeliveriesCmd.Flags().StringVar(&flagWebhookState, "state", "",
		"filter by state: pending|delivered|dead (empty = all)")
	webhookDeliveriesCmd.Flags().IntVar(&flagWebhookLimit, "limit", 50, "max rows")
	webhookCmd.AddCommand(webhookAddCmd, webhookListCmd, webhookRmCmd, webhookDeliveriesCmd, webhookReplayCmd)
	rootCmd.AddCommand(webhookCmd)
}
