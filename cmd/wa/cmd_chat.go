package main

import (
	"encoding/json"
	"fmt"
	"os"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"
)

// chatCmd is the `wa chat` parent for chat-state subcommands (FR-016, FR-017):
// archive, pin, mute, mark-unread.
var chatCmd = &cobra.Command{
	Use:   "chat",
	Short: "Manage chat-level state (archive, pin, mute, mark-unread)",
}

var (
	chatListLimit       int
	chatLastActiveLimit int
)

// chatListCmd / chatLastActiveCmd are read-only views over chat.list (#173).
// last-active is a thin alias with a smaller default limit for the common
// "what did I touch recently" question.
var chatListCmd = &cobra.Command{
	Use:   "list",
	Short: "List chats, most-recently-active first",
	RunE: func(cmd *cobra.Command, args []string) error {
		return runChatList(chatListLimit)
	},
}

var chatLastActiveCmd = &cobra.Command{
	Use:   "last-active",
	Short: "Show the most-recently-active chats (default 10)",
	RunE: func(cmd *cobra.Command, args []string) error {
		return runChatList(chatLastActiveLimit)
	},
}

func runChatList(limit int) error {
	params, _ := json.Marshal(map[string]any{"limit": limit})
	result, exitCode, err := callAndClose(flagSocket, "chat.list", params)
	if err != nil {
		return exiterr(exitCode, err)
	}
	if flagJSON {
		printNDJSONField("wa.chat.list/v1", "chats", result)
		return nil
	}
	printChatTable(result)
	return nil
}

func printChatTable(result json.RawMessage) {
	var resp struct {
		Chats []struct {
			JID           string `json:"jid"`
			PushName      string `json:"pushName"`
			LastMessageTS int64  `json:"lastMessageTs"`
			MessageCount  int64  `json:"messageCount"`
			IsGroup       bool   `json:"isGroup"`
		} `json:"chats"`
	}
	if err := json.Unmarshal(result, &resp); err != nil {
		fmt.Println(formatHuman("chat.list", result))
		return
	}
	if len(resp.Chats) == 0 {
		fmt.Println("No chats")
		return
	}
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	_, _ = fmt.Fprintln(w, "LAST\tMSGS\tKIND\tNAME\tJID")
	for _, c := range resp.Chats {
		last := time.Unix(c.LastMessageTS, 0).Format("2006-01-02 15:04")
		kind := "dm"
		if c.IsGroup {
			kind = "group"
		}
		name := c.PushName
		if name == "" {
			name = "-"
		}
		_, _ = fmt.Fprintf(w, "%s\t%d\t%s\t%s\t%s\n", last, c.MessageCount, kind, name, c.JID)
	}
	_ = w.Flush()
}

var (
	chatArchiveChat           string
	chatArchiveUnarchive      bool
	chatArchiveIdempotencyKey string
)

var chatArchiveCmd = &cobra.Command{
	Use:   "archive",
	Short: "Archive or unarchive a chat (FR-016)",
	Long:  "Archive hides a chat from the main list. --unarchive restores it. Repeated calls are server-side idempotent.",
	RunE: func(cmd *cobra.Command, args []string) error {
		if chatArchiveChat == "" {
			return exitf(64, "wa chat archive: --chat is required")
		}
		params := map[string]any{
			"chat":     chatArchiveChat,
			"archived": !chatArchiveUnarchive,
		}
		if chatArchiveIdempotencyKey != "" {
			params["idempotencyKey"] = chatArchiveIdempotencyKey
		}
		result, exitCode, err := callAndClose(flagSocket, "chat.archive", params)
		if err != nil {
			return exiterr(exitCode, err)
		}
		fmt.Println(formatResult("chat.archive", result, flagJSON))
		return nil
	},
}

var (
	chatPinChat           string
	chatPinUnpin          bool
	chatPinIdempotencyKey string
)

var chatPinCmd = &cobra.Command{
	Use:   "pin",
	Short: "Pin or unpin a chat (FR-016)",
	Long:  "Pin raises a chat to the top of the list. --unpin removes. WhatsApp caps pinned chats at 3; the 4th attempt returns -32000 upstream_error.",
	RunE: func(cmd *cobra.Command, args []string) error {
		if chatPinChat == "" {
			return exitf(64, "wa chat pin: --chat is required")
		}
		params := map[string]any{
			"chat":   chatPinChat,
			"pinned": !chatPinUnpin,
		}
		if chatPinIdempotencyKey != "" {
			params["idempotencyKey"] = chatPinIdempotencyKey
		}
		result, exitCode, err := callAndClose(flagSocket, "chat.pin", params)
		if err != nil {
			return exiterr(exitCode, err)
		}
		fmt.Println(formatResult("chat.pin", result, flagJSON))
		return nil
	},
}

var (
	chatMuteChat           string
	chatMuteUntil          string
	chatMuteDuration       time.Duration
	chatMuteUnmute         bool
	chatMuteIdempotencyKey string
)

var chatMuteCmd = &cobra.Command{
	Use:   "mute",
	Short: "Mute a chat until a deadline (FR-016)",
	Long: `Mute silences notifications until a deadline. Provide either --until
(RFC3339 timestamp) or --duration (e.g. 1h, 24h). --unmute clears any
mute; a past --until returns -32100 policy_refused.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		if chatMuteChat == "" {
			return exitf(64, "wa chat mute: --chat is required")
		}
		params := map[string]any{
			"chat": chatMuteChat,
		}
		switch {
		case chatMuteUnmute:
			// Omit untilUnix → unmute.
		case chatMuteUntil != "":
			t, err := time.Parse(time.RFC3339, chatMuteUntil)
			if err != nil {
				return exitf(64, "wa chat mute: --until must be RFC3339, got %q", chatMuteUntil)
			}
			params["untilUnix"] = t.Unix()
		case chatMuteDuration > 0:
			params["untilUnix"] = time.Now().Add(chatMuteDuration).Unix()
		default:
			return exitf(64, "wa chat mute: --until, --duration, or --unmute required")
		}
		if chatMuteIdempotencyKey != "" {
			params["idempotencyKey"] = chatMuteIdempotencyKey
		}
		result, exitCode, err := callAndClose(flagSocket, "chat.mute", params)
		if err != nil {
			return exiterr(exitCode, err)
		}
		fmt.Println(formatResult("chat.mute", result, flagJSON))
		return nil
	},
}

var (
	chatMarkUnreadChat           string
	chatMarkUnreadIdempotencyKey string
)

var chatMarkUnreadCmd = &cobra.Command{
	Use:   "mark-unread",
	Short: "Mark a chat as unread (FR-017)",
	RunE: func(cmd *cobra.Command, args []string) error {
		if chatMarkUnreadChat == "" {
			return exitf(64, "wa chat mark-unread: --chat is required")
		}
		params := map[string]any{"chat": chatMarkUnreadChat}
		if chatMarkUnreadIdempotencyKey != "" {
			params["idempotencyKey"] = chatMarkUnreadIdempotencyKey
		}
		result, exitCode, err := callAndClose(flagSocket, "chat.markUnread", params)
		if err != nil {
			return exiterr(exitCode, err)
		}
		fmt.Println(formatResult("chat.markUnread", result, flagJSON))
		return nil
	},
}

func init() {
	chatArchiveCmd.Flags().StringVar(&chatArchiveChat, "chat", "", "chat JID")
	chatArchiveCmd.Flags().BoolVar(&chatArchiveUnarchive, "unarchive", false, "unarchive instead of archive")
	chatArchiveCmd.Flags().StringVar(&chatArchiveIdempotencyKey, "idempotency-key", "", "FR-034a replay key")

	chatPinCmd.Flags().StringVar(&chatPinChat, "chat", "", "chat JID")
	chatPinCmd.Flags().BoolVar(&chatPinUnpin, "unpin", false, "unpin instead of pin")
	chatPinCmd.Flags().StringVar(&chatPinIdempotencyKey, "idempotency-key", "", "FR-034a replay key")

	chatMuteCmd.Flags().StringVar(&chatMuteChat, "chat", "", "chat JID")
	chatMuteCmd.Flags().StringVar(&chatMuteUntil, "until", "", "mute until RFC3339 timestamp (e.g. 2026-04-30T12:00:00Z)")
	chatMuteCmd.Flags().DurationVar(&chatMuteDuration, "duration", 0, "mute for this duration from now (e.g. 1h, 24h)")
	chatMuteCmd.Flags().BoolVar(&chatMuteUnmute, "unmute", false, "unmute the chat")
	chatMuteCmd.Flags().StringVar(&chatMuteIdempotencyKey, "idempotency-key", "", "FR-034a replay key")

	chatMarkUnreadCmd.Flags().StringVar(&chatMarkUnreadChat, "chat", "", "chat JID")
	chatMarkUnreadCmd.Flags().StringVar(&chatMarkUnreadIdempotencyKey, "idempotency-key", "", "FR-034a replay key")

	chatListCmd.Flags().IntVar(&chatListLimit, "limit", 200, "max chats (≤1000)")
	chatLastActiveCmd.Flags().IntVar(&chatLastActiveLimit, "limit", 10, "max chats (≤1000)")

	chatCmd.AddCommand(chatListCmd)
	chatCmd.AddCommand(chatLastActiveCmd)
	chatCmd.AddCommand(chatArchiveCmd)
	chatCmd.AddCommand(chatPinCmd)
	chatCmd.AddCommand(chatMuteCmd)
	chatCmd.AddCommand(chatMarkUnreadCmd)
	rootCmd.AddCommand(chatCmd)
}
