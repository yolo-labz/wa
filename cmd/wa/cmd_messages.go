package main

import (
	"encoding/json"

	"github.com/spf13/cobra"
)

var messagesLimit int

var messagesCmd = &cobra.Command{
	Use:   "messages",
	Short: "List recent messages across all chats",
	RunE: func(cmd *cobra.Command, args []string) error {
		params, _ := json.Marshal(map[string]any{"limit": messagesLimit})
		result, exitCode, err := callAndClose(flagSocket, "messages", params)
		if err != nil {
			return exiterr(exitCode, err)
		}
		if flagJSON {
			printNDJSON("wa.messages/v1", result)
			return nil
		}
		printMessageTable(result)
		return nil
	},
}

var (
	messagesListChat   string
	messagesListType   string
	messagesListFromMe bool
	messagesListSince  string
	messagesListUntil  string
	messagesListLimit  int
)

// messagesListCmd is the filtered counterpart to `wa messages`. Where the
// bare command dumps the most-recent rows across every chat, `list` narrows
// by chat, media kind, direction, and time window (#173).
var messagesListCmd = &cobra.Command{
	Use:   "list",
	Short: "List messages with filters (chat, media-type, direction, time, limit)",
	Long: `Filtered message query. Unlike 'wa messages' (recent across all chats),
list narrows by --chat, --media-type (audio|video|image|pdf|<mime>),
--from-me/--from-me=false, and --since/--until (RFC3339).

  wa messages list --chat 5581...@s.whatsapp.net --media-type audio --limit 20
  wa messages list --from-me --since 2026-01-01T00:00:00Z`,
	RunE: func(cmd *cobra.Command, args []string) error {
		since, err := parseTimeFlag("wa messages list", "since", messagesListSince)
		if err != nil {
			return err
		}
		until, err := parseTimeFlag("wa messages list", "until", messagesListUntil)
		if err != nil {
			return err
		}
		body := map[string]any{"limit": messagesListLimit}
		if messagesListChat != "" {
			body["chat"] = messagesListChat
		}
		if messagesListType != "" {
			body["mediaType"] = messagesListType
		}
		// Tri-state --from-me: only forward the filter when the operator
		// actually set it, so the default lists both directions.
		if cmd.Flags().Changed("from-me") {
			body["fromMe"] = messagesListFromMe
		}
		if since > 0 {
			body["since"] = since
		}
		if until > 0 {
			body["until"] = until
		}
		params, _ := json.Marshal(body)
		result, exitCode, err := callAndClose(flagSocket, "messages.list", params)
		if err != nil {
			return exiterr(exitCode, err)
		}
		if flagJSON {
			printNDJSON("wa.messages.list/v1", result)
			return nil
		}
		printMessageTable(result)
		return nil
	},
}

func init() {
	messagesCmd.Flags().IntVar(&messagesLimit, "limit", 50, "max messages to return")

	messagesListCmd.Flags().StringVar(&messagesListChat, "chat", "", "filter by chat JID")
	messagesListCmd.Flags().StringVar(&messagesListType, "media-type", "", "filter by media type (audio|video|image|pdf|<mime>)")
	messagesListCmd.Flags().BoolVar(&messagesListFromMe, "from-me", false, "only my messages (--from-me=false for inbound only)")
	messagesListCmd.Flags().StringVar(&messagesListSince, "since", "", "lower time bound (RFC3339)")
	messagesListCmd.Flags().StringVar(&messagesListUntil, "until", "", "upper time bound (RFC3339)")
	messagesListCmd.Flags().IntVar(&messagesListLimit, "limit", 50, "max messages (≤500)")
	messagesCmd.AddCommand(messagesListCmd)
}
