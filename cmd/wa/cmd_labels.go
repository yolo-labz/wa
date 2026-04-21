package main

import (
	"encoding/json"
	"fmt"

	"github.com/spf13/cobra"
)

var (
	labelsName      string
	labelsColor     int
	labelsID        string
	labelsLabelID   string
	labelsChat      string
	labelsMessageID string
)

var labelsCmd = &cobra.Command{
	Use:   "labels",
	Short: "Manage WhatsApp Business labels (behind --features labels flag)",
}

var labelsListCmd = &cobra.Command{
	Use:   "list",
	Short: "List labels for the paired Business account",
	RunE: func(cmd *cobra.Command, args []string) error {
		result, exitCode, err := callAndClose(flagSocket, "labels.list", json.RawMessage(`{}`))
		if err != nil {
			return exiterr(exitCode, err)
		}
		fmt.Println(formatResult("labels.list", result, flagJSON))
		return nil
	},
}

var labelsCreateCmd = &cobra.Command{
	Use:   "create",
	Short: "Create a new label",
	RunE: func(cmd *cobra.Command, args []string) error {
		if labelsName == "" {
			return exitf(64, "wa labels create: --name is required")
		}
		raw, _ := json.Marshal(map[string]any{"name": labelsName, "colorIndex": labelsColor})
		result, exitCode, err := callAndClose(flagSocket, "labels.create", raw)
		if err != nil {
			return exiterr(exitCode, err)
		}
		fmt.Println(formatResult("labels.create", result, flagJSON))
		return nil
	},
}

var labelsDeleteCmd = &cobra.Command{
	Use:   "delete",
	Short: "Delete a label by id",
	RunE: func(cmd *cobra.Command, args []string) error {
		if labelsID == "" {
			return exitf(64, "wa labels delete: --id is required")
		}
		raw, _ := json.Marshal(map[string]any{"id": labelsID})
		result, exitCode, err := callAndClose(flagSocket, "labels.delete", raw)
		if err != nil {
			return exiterr(exitCode, err)
		}
		fmt.Println(formatResult("labels.delete", result, flagJSON))
		return nil
	},
}

var labelsAssignCmd = &cobra.Command{
	Use:   "assign",
	Short: "Attach a label to a chat (or a specific message via --message-id)",
	RunE: func(cmd *cobra.Command, args []string) error {
		return sendLabelAssignment("labels.assign")
	},
}

var labelsUnassignCmd = &cobra.Command{
	Use:   "unassign",
	Short: "Detach a label from a chat or a specific message",
	RunE: func(cmd *cobra.Command, args []string) error {
		return sendLabelAssignment("labels.unassign")
	},
}

func sendLabelAssignment(method string) error {
	if labelsLabelID == "" || labelsChat == "" {
		return exitf(64, "wa %s: --label-id and --chat are required", method)
	}
	params := map[string]any{
		"labelID": labelsLabelID,
		"chat":    labelsChat,
	}
	if labelsMessageID != "" {
		params["messageID"] = labelsMessageID
	}
	raw, _ := json.Marshal(params)
	result, exitCode, err := callAndClose(flagSocket, method, raw)
	if err != nil {
		return exiterr(exitCode, err)
	}
	fmt.Println(formatResult(method, result, flagJSON))
	return nil
}

func init() {
	labelsCreateCmd.Flags().StringVar(&labelsName, "name", "", "label name")
	labelsCreateCmd.Flags().IntVar(&labelsColor, "color", 0, "palette index [0,19]")

	labelsDeleteCmd.Flags().StringVar(&labelsID, "id", "", "label id")

	labelsAssignCmd.Flags().StringVar(&labelsLabelID, "label-id", "", "label id")
	labelsAssignCmd.Flags().StringVar(&labelsChat, "chat", "", "chat JID")
	labelsAssignCmd.Flags().StringVar(&labelsMessageID, "message-id", "", "message id (for message-level assignment)")

	labelsUnassignCmd.Flags().StringVar(&labelsLabelID, "label-id", "", "label id")
	labelsUnassignCmd.Flags().StringVar(&labelsChat, "chat", "", "chat JID")
	labelsUnassignCmd.Flags().StringVar(&labelsMessageID, "message-id", "", "message id (for message-level assignment)")

	labelsCmd.AddCommand(labelsListCmd)
	labelsCmd.AddCommand(labelsCreateCmd)
	labelsCmd.AddCommand(labelsDeleteCmd)
	labelsCmd.AddCommand(labelsAssignCmd)
	labelsCmd.AddCommand(labelsUnassignCmd)
	rootCmd.AddCommand(labelsCmd)
}
