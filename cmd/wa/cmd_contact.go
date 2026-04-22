package main

import (
	"encoding/json"
	"fmt"

	"github.com/spf13/cobra"
)

// contactCmd is the `wa contact` parent for FR-018/FR-019 block-list
// subcommands: block, unblock, blocklist.
var contactCmd = &cobra.Command{
	Use:   "contact",
	Short: "Manage the server-side blocklist (block, unblock, blocklist)",
}

var (
	contactBlockJID            string
	contactBlockIdempotencyKey string
)

var contactBlockCmd = &cobra.Command{
	Use:   "block",
	Short: "Block a contact (FR-018)",
	Long: `Block refuses future send / sendMedia / react / send.reply to the
target JID with -32100 policy_refused at the socket boundary. Writes an
AuditBlock entry. Repeats are server-side idempotent.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		if contactBlockJID == "" {
			return exitf(64, "wa contact block: --jid is required")
		}
		params := map[string]any{"jid": contactBlockJID}
		if contactBlockIdempotencyKey != "" {
			params["idempotencyKey"] = contactBlockIdempotencyKey
		}
		result, exitCode, err := callAndClose(flagSocket, "contact.block", params)
		if err != nil {
			return exiterr(exitCode, err)
		}
		fmt.Println(formatResult("contact.block", result, flagJSON))
		return nil
	},
}

var (
	contactUnblockJID            string
	contactUnblockIdempotencyKey string
)

var contactUnblockCmd = &cobra.Command{
	Use:   "unblock",
	Short: "Unblock a contact (FR-018)",
	Long:  "Removes the target from the server-side blocklist and writes an AuditUnblock entry.",
	RunE: func(cmd *cobra.Command, args []string) error {
		if contactUnblockJID == "" {
			return exitf(64, "wa contact unblock: --jid is required")
		}
		params := map[string]any{"jid": contactUnblockJID}
		if contactUnblockIdempotencyKey != "" {
			params["idempotencyKey"] = contactUnblockIdempotencyKey
		}
		result, exitCode, err := callAndClose(flagSocket, "contact.unblock", params)
		if err != nil {
			return exiterr(exitCode, err)
		}
		fmt.Println(formatResult("contact.unblock", result, flagJSON))
		return nil
	},
}

var contactBlocklistCmd = &cobra.Command{
	Use:   "blocklist",
	Short: "Print the live server-side blocklist (FR-019)",
	Long: `Reads the live blocklist from the server — never a local cache —
so out-of-band unblocks from the phone UI are reflected immediately.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		result, exitCode, err := callAndClose(flagSocket, "contact.blocklist", map[string]any{})
		if err != nil {
			return exiterr(exitCode, err)
		}
		if flagJSON {
			fmt.Println(formatResult("contact.blocklist", result, true))
			return nil
		}
		var out struct {
			Blocked []string `json:"blocked"`
		}
		if err := json.Unmarshal(result, &out); err != nil {
			return exiterr(70, fmt.Errorf("wa contact blocklist: decode: %w", err))
		}
		if len(out.Blocked) == 0 {
			fmt.Println("(no blocked contacts)")
			return nil
		}
		for _, jid := range out.Blocked {
			fmt.Println(jid)
		}
		return nil
	},
}

func init() {
	contactBlockCmd.Flags().StringVar(&contactBlockJID, "jid", "", "contact JID to block (required)")
	contactBlockCmd.Flags().StringVar(&contactBlockIdempotencyKey, "idempotency-key", "", "FR-034a replay key")

	contactUnblockCmd.Flags().StringVar(&contactUnblockJID, "jid", "", "contact JID to unblock (required)")
	contactUnblockCmd.Flags().StringVar(&contactUnblockIdempotencyKey, "idempotency-key", "", "FR-034a replay key")

	contactCmd.AddCommand(contactBlockCmd)
	contactCmd.AddCommand(contactUnblockCmd)
	contactCmd.AddCommand(contactBlocklistCmd)
	rootCmd.AddCommand(contactCmd)
}
