package main

import (
	"encoding/json"
	"fmt"

	"github.com/spf13/cobra"
)

// syncCmd is the `wa sync` parent for on-demand history sync visibility
// (#174): force an immediate pull, or inspect the sync engine's state.
var syncCmd = &cobra.Command{
	Use:   "sync",
	Short: "Force and inspect on-demand history sync",
}

var (
	syncForceChat  string
	syncForceCount int
)

var syncForceCmd = &cobra.Command{
	Use:   "force",
	Short: "Trigger an immediate history pull from WhatsApp's servers",
	Long: `Force the daemon to request recent history from WhatsApp now, instead of
waiting for the normal sync cadence. Use this when new messages are
visible on your phone but the daemon's database has not caught up.

With --chat, the command blocks until a matching response lands (or the
~30s deadline elapses) and reports how many messages arrived. Without
--chat it fires a global newest-N pull and returns immediately; the
messages persist in the background — poll "wa sync status" or
"wa chat list" to watch them land.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		params := map[string]any{}
		if syncForceChat != "" {
			params["chat"] = syncForceChat
		}
		if syncForceCount > 0 {
			params["count"] = syncForceCount
		}
		result, exitCode, err := callAndClose(flagSocket, "sync.force", params)
		if err != nil {
			return exiterr(exitCode, err)
		}
		if flagJSON {
			fmt.Println(string(result))
			return nil
		}
		printSyncForce(result)
		return nil
	},
}

func printSyncForce(result json.RawMessage) {
	var r struct {
		Requested bool   `json:"requested"`
		Connected bool   `json:"connected"`
		Chat      string `json:"chat"`
		Count     int    `json:"count"`
		Received  int    `json:"received"`
		Delivered bool   `json:"delivered"`
		TimedOut  bool   `json:"timedOut"`
		Async     bool   `json:"async"`
	}
	if err := json.Unmarshal(result, &r); err != nil {
		fmt.Println(string(result))
		return
	}
	target := r.Chat
	if target == "" {
		target = "(all chats)"
	}
	fmt.Printf("requested:  %t (count %d) for %s\n", r.Requested, r.Count, target)
	switch {
	case r.Async:
		fmt.Println("status:     fired global pull; messages persist in the background — poll `wa sync status`")
	case r.Delivered:
		fmt.Printf("status:     delivered — %d message(s) synced for this chat\n", r.Received)
	case r.TimedOut:
		fmt.Println("status:     no response within the deadline; the pull may still land later")
	}
}

var syncStatusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show the on-demand history sync engine state",
	RunE: func(cmd *cobra.Command, args []string) error {
		result, exitCode, err := callAndClose(flagSocket, "sync.status", nil)
		if err != nil {
			return exiterr(exitCode, err)
		}
		if flagJSON {
			fmt.Println(string(result))
			return nil
		}
		var s struct {
			Syncing           bool `json:"syncing"`
			InFlightForceReqs int  `json:"inFlightForceReqs"`
			QueueDepth        int  `json:"queueDepth"`
			QueueCap          int  `json:"queueCap"`
		}
		if err := json.Unmarshal(result, &s); err != nil {
			return exiterr(1, fmt.Errorf("decode sync status: %w", err))
		}
		fmt.Printf("syncing:        %t\n", s.Syncing)
		fmt.Printf("in-flight:      %d force request(s)\n", s.InFlightForceReqs)
		fmt.Printf("queue:          %d / %d blobs\n", s.QueueDepth, s.QueueCap)
		return nil
	},
}

func init() {
	syncForceCmd.Flags().StringVar(&syncForceChat, "chat", "", "target chat JID (omit for a global newest-N pull)")
	syncForceCmd.Flags().IntVar(&syncForceCount, "count", 0, "max messages to pull (1-50, default 50)")
	syncCmd.AddCommand(syncForceCmd)
	syncCmd.AddCommand(syncStatusCmd)
	rootCmd.AddCommand(syncCmd)
}
