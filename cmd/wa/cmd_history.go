package main

import (
	"encoding/json"
	"fmt"
	"os"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"
)

var (
	historyChat   string
	historyLimit  int
	historyBefore string
)

var historyCmd = &cobra.Command{
	Use:   "history",
	Short: "Show message history for a chat",
	RunE: func(cmd *cobra.Command, args []string) error {
		if historyChat == "" {
			return exitf(64, "wa history: --chat is required")
		}
		params := map[string]any{
			"chat":  historyChat,
			"limit": historyLimit,
		}
		if historyBefore != "" {
			params["before"] = historyBefore
		}
		raw, _ := json.Marshal(params)
		result, exitCode, err := callAndClose(flagSocket, "history", raw)
		if err != nil {
			return exiterr(exitCode, err)
		}

		if flagJSON {
			printNDJSON("wa.history/v1", result)
			return nil
		}

		printMessageTable(result)
		return nil
	},
}

func init() {
	historyCmd.Flags().StringVar(&historyChat, "chat", "", "chat JID")
	historyCmd.Flags().IntVar(&historyLimit, "limit", 50, "max messages to return")
	historyCmd.Flags().StringVar(&historyBefore, "before", "", "cursor: message ID to paginate from")
}

func printMessageTable(result json.RawMessage) {
	var resp struct {
		Messages []struct {
			Timestamp int64  `json:"timestamp"`
			SenderJID string `json:"senderJid"`
			Body      string `json:"body"`
			ChatJID   string `json:"chatJid"`
			IsFromMe  bool   `json:"isFromMe"`
		} `json:"messages"`
	}
	if err := json.Unmarshal(result, &resp); err != nil {
		fmt.Println(formatHuman("history", result))
		return
	}
	if len(resp.Messages) == 0 {
		fmt.Println("No messages")
		return
	}
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	_, _ = fmt.Fprintln(w, "TIME\tFROM\tBODY")
	for _, m := range resp.Messages {
		ts := time.Unix(m.Timestamp, 0).Format("2006-01-02 15:04")
		sender := m.SenderJID
		if m.IsFromMe {
			sender = "me"
		}
		body := m.Body
		if len(body) > 80 {
			body = body[:77] + "..."
		}
		_, _ = fmt.Fprintf(w, "%s\t%s\t%s\n", ts, sender, body)
	}
	_ = w.Flush()
}

func printNDJSON(schema string, result json.RawMessage) {
	var resp struct {
		Messages []json.RawMessage `json:"messages"`
	}
	if err := json.Unmarshal(result, &resp); err != nil {
		fmt.Println(formatJSON(schema, result))
		return
	}
	for _, m := range resp.Messages {
		var obj map[string]any
		if err := json.Unmarshal(m, &obj); err != nil {
			continue
		}
		obj["schema"] = schema
		out, _ := json.Marshal(obj)
		fmt.Println(string(out))
	}
}
