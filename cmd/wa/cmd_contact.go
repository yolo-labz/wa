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

// contactLidCmd resolves a phone-number JID to its LID. Spec 106.
var contactLidCmd = &cobra.Command{
	Use:   "lid <pn>",
	Short: "Resolve a phone-number JID to its LID (`<digits>@lid`)",
	Long: `Calls contact.resolve with the supplied phone-number JID and prints
the LID counterpart. The PN may be passed as a digit string ("5511999999999"),
an E.164 string ("+5511999999999"), or the canonical form
("5511999999999@s.whatsapp.net").

Exit code 0 with empty stdout indicates "no mapping known yet" — typical for
a contact who has never been seen on this device. The command does NOT fail
in that case; callers branch on stdout being empty.`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		params, _ := json.Marshal(map[string]any{"jid": args[0]})
		result, exitCode, err := callAndClose(flagSocket, "contact.resolve", params)
		if err != nil {
			return exiterr(exitCode, err)
		}
		return printContactResolveAlt("contact.resolve", result, "lid", flagJSON)
	},
}

// contactPnCmd resolves a LID to its phone-number JID. Spec 106.
var contactPnCmd = &cobra.Command{
	Use:   "pn <lid>",
	Short: "Resolve a LID to its phone-number JID",
	Long: `Calls contact.resolve with the supplied LID and prints the PN
counterpart. The LID is passed as the canonical form ("66448177246461@lid").

Exit code 0 with empty stdout indicates "no mapping known yet" — many LIDs
have no PN counterpart from this account's perspective by design (the
WhatsApp privacy property LIDs were engineered for). Callers branch on
stdout being empty.`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		params, _ := json.Marshal(map[string]any{"jid": args[0]})
		result, exitCode, err := callAndClose(flagSocket, "contact.resolve", params)
		if err != nil {
			return exiterr(exitCode, err)
		}
		return printContactResolveAlt("contact.resolve", result, "pn", flagJSON)
	},
}

// printContactResolveAlt formats the contact.resolve result for the
// `wa contact lid` / `wa contact pn` subcommands. In `--json` mode it
// prints the full envelope; otherwise it prints only the resolved
// alternate-namespace JID followed by a newline (or nothing when no
// mapping is known yet).
func printContactResolveAlt(method string, result []byte, wantKind string, asJSON bool) error {
	if asJSON {
		fmt.Println(formatResult(method, result, true))
		return nil
	}
	var out struct {
		Input string `json:"input"`
		Alt   string `json:"alt"`
		Kind  string `json:"kind"`
		Known bool   `json:"known"`
	}
	if err := json.Unmarshal(result, &out); err != nil {
		return exiterr(70, fmt.Errorf("wa contact %s: decode: %w", wantKind, err))
	}
	if out.Kind != wantKind {
		// The user asked for the OTHER direction — `wa contact lid <pn>`
		// expects kind=pn (input is PN, alt is LID), `wa contact pn <lid>`
		// expects kind=lid. Disagreement means the input was the wrong
		// shape; surface that loudly so callers can re-issue.
		return exitf(64, "wa contact %s: input was a %s, expected %s",
			wantKind, out.Kind, opposite(wantKind))
	}
	if out.Known {
		fmt.Println(out.Alt)
	}
	return nil
}

// opposite flips the wantKind label for human-readable error messages.
func opposite(kind string) string {
	if kind == "lid" {
		return "pn"
	}
	return "lid"
}

func init() {
	contactBlockCmd.Flags().StringVar(&contactBlockJID, "jid", "", "contact JID to block (required)")
	contactBlockCmd.Flags().StringVar(&contactBlockIdempotencyKey, "idempotency-key", "", "FR-034a replay key")

	contactUnblockCmd.Flags().StringVar(&contactUnblockJID, "jid", "", "contact JID to unblock (required)")
	contactUnblockCmd.Flags().StringVar(&contactUnblockIdempotencyKey, "idempotency-key", "", "FR-034a replay key")

	contactCmd.AddCommand(contactBlockCmd)
	contactCmd.AddCommand(contactUnblockCmd)
	contactCmd.AddCommand(contactBlocklistCmd)
	contactCmd.AddCommand(contactLidCmd)
	contactCmd.AddCommand(contactPnCmd)
	rootCmd.AddCommand(contactCmd)
}
