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
	Short: "Interact with polls (create, vote)",
	Long: `poll exposes poll interactions over the daemon.

  wa poll vote --chat <jid> --poll-id <id> --option <n> [--option <n>...]

In v2.0.0 the adapter returns -32000 upstream_error for any well-formed
call; shape errors come back as -32602 invalid_params (CLI exit 64).`,
}

var (
	pollVoteChat    string
	pollVotePollID  string
	pollVoteOptions []int

	pollCreateChat       string
	pollCreateQuestion   string
	pollCreateOptions    []string
	pollCreateSelectable int
)

var pollCreateCmd = &cobra.Command{
	Use:   "create",
	Short: "Send a poll to a chat (FR-032)",
	Long: `create sends a poll message.

  --chat        the chat JID to post the poll in (required)
  --question    the poll question (required)
  --option      an option; repeat for each, 2-12 total, no duplicates
  --selectable  how many options one voter may pick (default 1)

  wa poll create --chat 5511999@s.whatsapp.net --question "lunch?" \
    --option pizza --option sushi`,
	RunE: func(cmd *cobra.Command, args []string) error {
		params := map[string]any{
			"chat":       pollCreateChat,
			"question":   pollCreateQuestion,
			"options":    pollCreateOptions,
			"selectable": pollCreateSelectable,
		}
		result, exitCode, err := callAndClose(flagSocket, "poll.create", params)
		if err != nil {
			return exiterr(exitCode, err)
		}
		fmt.Println(formatResult("poll.create", result, flagJSON))
		return nil
	},
}

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

	pollCreateCmd.Flags().StringVar(&pollCreateChat, "chat", "", "chat JID to post the poll in (required)")
	pollCreateCmd.Flags().StringVar(&pollCreateQuestion, "question", "", "poll question (required)")
	pollCreateCmd.Flags().StringArrayVar(&pollCreateOptions, "option", nil, "poll option (repeatable, 2-12)")
	pollCreateCmd.Flags().IntVar(&pollCreateSelectable, "selectable", 1, "how many options one voter may pick")
	_ = pollCreateCmd.MarkFlagRequired("chat")
	_ = pollCreateCmd.MarkFlagRequired("question")
	_ = pollCreateCmd.MarkFlagRequired("option")

	pollCmd.AddCommand(pollCreateCmd)
	pollCmd.AddCommand(pollVoteCmd)
	rootCmd.AddCommand(pollCmd)
}
