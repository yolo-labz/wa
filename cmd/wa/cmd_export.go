package main

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

var (
	exportChat  string
	exportSince string
	exportUntil string
	exportLimit int
)

var exportCmd = &cobra.Command{
	Use:   "export",
	Short: "Export messages for a chat as NDJSON, optionally time/limit bounded",
	Long: `Export a chat's messages oldest-first as NDJSON. Narrow the window with
--since/--until (RFC3339) and cap rows with --limit (0 = all, up to 100000).

  wa export --chat 5581...@s.whatsapp.net
  wa export --chat 5581...@s.whatsapp.net --since 2026-01-01T00:00:00Z --limit 500`,
	RunE: func(cmd *cobra.Command, args []string) error {
		if exportChat == "" {
			return exitf(64, "wa export: --chat is required")
		}
		since, err := parseTimeFlag("wa export", "since", exportSince)
		if err != nil {
			return err
		}
		until, err := parseTimeFlag("wa export", "until", exportUntil)
		if err != nil {
			return err
		}
		body := map[string]any{"chat": exportChat}
		if since > 0 {
			body["since"] = since
		}
		if until > 0 {
			body["until"] = until
		}
		if exportLimit > 0 {
			body["limit"] = exportLimit
		}
		params, _ := json.Marshal(body)
		result, exitCode, err := callAndClose(flagSocket, "export", params)
		if err != nil {
			return exiterr(exitCode, err)
		}
		// A zero-row export is indistinguishable from a typo'd / nonexistent
		// JID in the current schema (there is no chats table — only the
		// messages table, so "chat exists but empty" and "chat never seen"
		// both yield 0 rows). Rather than the old silent exit-0 that made
		// every JID-guess miss look like success (#177), exit 64 with a
		// stderr message so callers brute-forcing JID variants can tell a
		// miss from a hit. stdout stays clean for the pipe; main() prints
		// the returned error to stderr.
		n := printNDJSON("wa.export/v1", result)
		// A chat JID names one half of a conversation, not the conversation:
		// outbound lands under the phone JID and replies under the LID, so a
		// complete-looking export can be missing every answer. Print the
		// counterpart on stderr, leaving stdout a clean NDJSON pipe. Issue
		// #355.
		printLinkedChatNote(result)
		if n == 0 {
			return exitf(64, "wa export: 0 messages for %q — chat is empty or not "+
				"in the local store; re-check the JID (try `wa contacts search` or "+
				"`wa chat list`)", exportChat)
		}
		return nil
	},
}

// printLinkedChatNote writes the daemon's `linked` advisory to stderr when
// the exported chat has a PN↔LID counterpart holding rows the export did
// not return. Silent on absence, on a daemon too old to send the field, and
// on any decode failure — an advisory that cannot be read is not an error
// worth interrupting a successful export for.
func printLinkedChatNote(result json.RawMessage) {
	var resp struct {
		Linked *struct {
			Chat     string `json:"chat"`
			Messages int    `json:"messages"`
		} `json:"linked"`
	}
	if err := json.Unmarshal(result, &resp); err != nil || resp.Linked == nil {
		return
	}
	if resp.Linked.Chat == "" || resp.Linked.Messages == 0 {
		return
	}
	fmt.Fprintf(os.Stderr,
		"note: %s has a linked chat %s holding %d message(s) not shown here — "+
			"export it too for the full conversation\n",
		exportChat, resp.Linked.Chat, resp.Linked.Messages)
}

func init() {
	exportCmd.Flags().StringVar(&exportChat, "chat", "", "chat JID to export")
	exportCmd.Flags().StringVar(&exportSince, "since", "", "lower time bound (RFC3339)")
	exportCmd.Flags().StringVar(&exportUntil, "until", "", "upper time bound (RFC3339)")
	exportCmd.Flags().IntVar(&exportLimit, "limit", 0, "max messages (0 = all, ≤100000)")
}
