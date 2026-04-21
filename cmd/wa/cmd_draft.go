package main

import (
	"encoding/json"
	"fmt"

	"github.com/spf13/cobra"
)

var (
	draftID         string
	draftState      string
	draftLimit      int
	draftDecider    string
	draftRejectWhy  string
)

var draftCmd = &cobra.Command{
	Use:   "draft",
	Short: "Human-review draft queue operations",
}

var draftListCmd = &cobra.Command{
	Use:   "list",
	Short: "List drafts (default: pending_review)",
	RunE: func(cmd *cobra.Command, args []string) error {
		params, _ := json.Marshal(map[string]any{"state": draftState, "limit": draftLimit})
		result, exitCode, err := callAndClose(flagSocket, "draft.list", params)
		if err != nil {
			return exiterr(exitCode, err)
		}
		fmt.Println(formatResult("draft.list", result, flagJSON))
		return nil
	},
}

var draftGetCmd = &cobra.Command{
	Use:   "get",
	Short: "Fetch a single draft by id",
	RunE: func(cmd *cobra.Command, args []string) error {
		if draftID == "" {
			return exitf(64, "wa draft get: --id is required")
		}
		params, _ := json.Marshal(map[string]any{"id": draftID})
		result, exitCode, err := callAndClose(flagSocket, "draft.get", params)
		if err != nil {
			return exiterr(exitCode, err)
		}
		fmt.Println(formatResult("draft.get", result, flagJSON))
		return nil
	},
}

var draftApproveCmd = &cobra.Command{
	Use:   "approve",
	Short: "Approve a pending draft (decider: cli | channel)",
	RunE: func(cmd *cobra.Command, args []string) error {
		if draftID == "" {
			return exitf(64, "wa draft approve: --id is required")
		}
		params, _ := json.Marshal(map[string]any{"id": draftID, "decider": draftDecider})
		result, exitCode, err := callAndClose(flagSocket, "draft.approve", params)
		if err != nil {
			return exiterr(exitCode, err)
		}
		fmt.Println(formatResult("draft.approve", result, flagJSON))
		return nil
	},
}

var draftRejectCmd = &cobra.Command{
	Use:   "reject",
	Short: "Reject a pending draft with reason",
	RunE: func(cmd *cobra.Command, args []string) error {
		if draftID == "" {
			return exitf(64, "wa draft reject: --id is required")
		}
		params, _ := json.Marshal(map[string]any{
			"id":      draftID,
			"decider": draftDecider,
			"reason":  draftRejectWhy,
		})
		result, exitCode, err := callAndClose(flagSocket, "draft.reject", params)
		if err != nil {
			return exiterr(exitCode, err)
		}
		fmt.Println(formatResult("draft.reject", result, flagJSON))
		return nil
	},
}

func init() {
	draftListCmd.Flags().StringVar(&draftState, "state", "pending_review", "state filter")
	draftListCmd.Flags().IntVar(&draftLimit, "limit", 50, "max results")
	draftGetCmd.Flags().StringVar(&draftID, "id", "", "draft id")
	draftApproveCmd.Flags().StringVar(&draftID, "id", "", "draft id")
	draftApproveCmd.Flags().StringVar(&draftDecider, "decider", "cli", "cli | channel")
	draftRejectCmd.Flags().StringVar(&draftID, "id", "", "draft id")
	draftRejectCmd.Flags().StringVar(&draftDecider, "decider", "cli", "cli | channel")
	draftRejectCmd.Flags().StringVar(&draftRejectWhy, "reason", "", "reject reason")

	draftCmd.AddCommand(draftListCmd)
	draftCmd.AddCommand(draftGetCmd)
	draftCmd.AddCommand(draftApproveCmd)
	draftCmd.AddCommand(draftRejectCmd)
	rootCmd.AddCommand(draftCmd)
}
