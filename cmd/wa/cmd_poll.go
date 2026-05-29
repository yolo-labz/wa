package main

import (
	"fmt"

	"github.com/spf13/cobra"
)

// pollCmd is the `wa poll` parent for poll interactions (FR-032). Thin
// client: the single `vote` subcommand makes one poll.vote JSON-RPC call
// and prints the result.
var pollCmd = &cobra.Command{
	Use:   "poll",
	Short: "Interact with polls (vote)",
	Long: `poll exposes poll interactions over the daemon.

  wa poll vote --chat <jid> --poll-id <id> --option <n> [--option <n>...]

In v2.0.0 the adapter returns -32000 upstream_error for any well-formed
call; shape errors come back as -32602 invalid_params (CLI exit 64).`,
}

var (
	pollVoteChat    string
	pollVotePollID  string
	pollVoteOptions []int
)

var pollVoteCmd = &cobra.Command{
	Use:   "vote",
	Short: "Cast a vote on a poll (FR-032)",
	Long: `vote submits the selected option indices for a poll message.

  --chat     the chat JID the poll lives in (required)
  --poll-id  the poll message ID (required)
  --option   a zero-based option index; repeat for multi-select, e.g.
             --option 0 --option 2. Passing no --option clears the vote.

The indices are forwarded verbatim as the "selected" array; the daemon
maps them onto the poll's options.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		// IntSliceVar defaults to nil; normalise to a non-nil empty slice
		// so the wire "selected" field is [] (vote-clear) rather than null.
		selected := pollVoteOptions
		if selected == nil {
			selected = []int{}
		}
		params := map[string]any{
			"chat":     pollVoteChat,
			"pollId":   pollVotePollID,
			"selected": selected,
		}
		result, exitCode, err := callAndClose(flagSocket, "poll.vote", params)
		if err != nil {
			return exiterr(exitCode, err)
		}
		fmt.Println(formatResult("poll.vote", result, flagJSON))
		return nil
	},
}

func init() {
	pollVoteCmd.Flags().StringVar(&pollVoteChat, "chat", "", "chat JID the poll lives in (required)")
	pollVoteCmd.Flags().StringVar(&pollVotePollID, "poll-id", "", "poll message ID (required)")
	pollVoteCmd.Flags().IntSliceVar(&pollVoteOptions, "option", nil, "zero-based option index (repeatable)")
	_ = pollVoteCmd.MarkFlagRequired("chat")
	_ = pollVoteCmd.MarkFlagRequired("poll-id")

	pollCmd.AddCommand(pollVoteCmd)
	rootCmd.AddCommand(pollCmd)
}
