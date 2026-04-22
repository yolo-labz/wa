package main

import (
	"fmt"

	"github.com/spf13/cobra"
)

// msgCmd is the `wa msg` parent for moderation subcommands (revoke, edit).
var msgCmd = &cobra.Command{
	Use:   "msg",
	Short: "Moderate already-sent messages (revoke, edit)",
}

var (
	msgRevokeChat           string
	msgRevokeID             string
	msgRevokeScope          string
	msgRevokeIdempotencyKey string
)

var msgRevokeCmd = &cobra.Command{
	Use:   "revoke",
	Short: "Revoke a previously-sent message (FR-014)",
	Long:  "Revoke removes a message. --scope=self wipes the local row only; --scope=everyone (default) emits a ProtocolMessage REVOKE so peers delete their copy.",
	RunE: func(cmd *cobra.Command, args []string) error {
		if msgRevokeChat == "" || msgRevokeID == "" {
			return exitf(64, "wa msg revoke: --chat and --messageId are required")
		}
		switch msgRevokeScope {
		case "self", "everyone":
		default:
			return exitf(64, "wa msg revoke: --scope must be self|everyone, got %q", msgRevokeScope)
		}
		params := map[string]any{
			"chat":      msgRevokeChat,
			"messageId": msgRevokeID,
			"scope":     msgRevokeScope,
		}
		if msgRevokeIdempotencyKey != "" {
			params["idempotencyKey"] = msgRevokeIdempotencyKey
		}
		result, exitCode, err := callAndClose(flagSocket, "message.revoke", params)
		if err != nil {
			return exiterr(exitCode, err)
		}
		fmt.Println(formatResult("message.revoke", result, flagJSON))
		return nil
	},
}

var (
	msgEditChat           string
	msgEditID             string
	msgEditBody           string
	msgEditIdempotencyKey string
)

var msgEditCmd = &cobra.Command{
	Use:   "edit",
	Short: "Rewrite a previously-sent message within the 15-min window (FR-015)",
	Long:  "Edit replaces the body of a message. Refused if the original is older than 15 minutes (-32100 policy_refused).",
	RunE: func(cmd *cobra.Command, args []string) error {
		if msgEditChat == "" || msgEditID == "" || msgEditBody == "" {
			return exitf(64, "wa msg edit: --chat, --messageId, and --body are required")
		}
		params := map[string]any{
			"chat":      msgEditChat,
			"messageId": msgEditID,
			"body":      msgEditBody,
		}
		if msgEditIdempotencyKey != "" {
			params["idempotencyKey"] = msgEditIdempotencyKey
		}
		result, exitCode, err := callAndClose(flagSocket, "message.edit", params)
		if err != nil {
			return exiterr(exitCode, err)
		}
		fmt.Println(formatResult("message.edit", result, flagJSON))
		return nil
	},
}

func init() {
	msgRevokeCmd.Flags().StringVar(&msgRevokeChat, "chat", "", "chat JID")
	msgRevokeCmd.Flags().StringVar(&msgRevokeID, "messageId", "", "message ID to revoke")
	msgRevokeCmd.Flags().StringVar(&msgRevokeScope, "scope", "everyone", "revoke scope: self|everyone")
	msgRevokeCmd.Flags().StringVar(&msgRevokeIdempotencyKey, "idempotency-key", "", "FR-034a replay key; same key + params replays cached result, same key + different params returns -32101")

	msgEditCmd.Flags().StringVar(&msgEditChat, "chat", "", "chat JID")
	msgEditCmd.Flags().StringVar(&msgEditID, "messageId", "", "message ID to edit")
	msgEditCmd.Flags().StringVar(&msgEditBody, "body", "", "replacement body")
	msgEditCmd.Flags().StringVar(&msgEditIdempotencyKey, "idempotency-key", "", "FR-034a replay key; same key + params replays cached result, same key + different params returns -32101")

	msgCmd.AddCommand(msgRevokeCmd)
	msgCmd.AddCommand(msgEditCmd)
	rootCmd.AddCommand(msgCmd)
}
