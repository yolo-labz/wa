package main

import (
	"encoding/json"
	"fmt"

	"github.com/spf13/cobra"
)

var (
	contactsJID       string
	contactsQuery     string
	contactsLimit     int
	contactsListLimit int
	contactsNotes     string
	contactsTags      []string
	contactsMode      string
)

var contactsCmd = &cobra.Command{
	Use:   "contacts",
	Short: "Contact directory operations",
}

var contactsLookupCmd = &cobra.Command{
	Use:   "lookup",
	Short: "Look up a contact by JID",
	RunE: func(cmd *cobra.Command, args []string) error {
		if contactsJID == "" {
			return exitf(64, "wa contacts lookup: --jid is required")
		}
		params, _ := json.Marshal(map[string]any{"jid": contactsJID})
		result, exitCode, err := callAndClose(flagSocket, "contacts.lookup", params)
		if err != nil {
			return exiterr(exitCode, err)
		}
		fmt.Println(formatResult("contacts.lookup", result, flagJSON))
		return nil
	},
}

var contactsSearchCmd = &cobra.Command{
	Use:   "search",
	Short: "Trigram-search the local contact directory",
	RunE: func(cmd *cobra.Command, args []string) error {
		if contactsQuery == "" {
			return exitf(64, "wa contacts search: --query is required")
		}
		params, _ := json.Marshal(map[string]any{"query": contactsQuery, "limit": contactsLimit})
		result, exitCode, err := callAndClose(flagSocket, "contacts.search", params)
		if err != nil {
			return exiterr(exitCode, err)
		}
		fmt.Println(formatResult("contacts.search", result, flagJSON))
		return nil
	},
}

var contactsListCmd = &cobra.Command{
	Use:   "list",
	Short: "List contacts, most-recently-changed first",
	RunE: func(cmd *cobra.Command, args []string) error {
		params, _ := json.Marshal(map[string]any{"limit": contactsListLimit})
		result, exitCode, err := callAndClose(flagSocket, "contacts.list", params)
		if err != nil {
			return exiterr(exitCode, err)
		}
		fmt.Println(formatResult("contacts.list", result, flagJSON))
		return nil
	},
}

var contactsAnnotateCmd = &cobra.Command{
	Use:   "annotate",
	Short: "Attach notes and tags to a contact",
	RunE: func(cmd *cobra.Command, args []string) error {
		if contactsJID == "" {
			return exitf(64, "wa contacts annotate: --jid is required")
		}
		params, _ := json.Marshal(map[string]any{
			"jid":   contactsJID,
			"notes": contactsNotes,
			"tags":  contactsTags,
		})
		result, exitCode, err := callAndClose(flagSocket, "contacts.annotate", params)
		if err != nil {
			return exiterr(exitCode, err)
		}
		fmt.Println(formatResult("contacts.annotate", result, flagJSON))
		return nil
	},
}

var contactsSyncCmd = &cobra.Command{
	Use:   "sync",
	Short: "Trigger a contact mirror sync (delta | full)",
	RunE: func(cmd *cobra.Command, args []string) error {
		params, _ := json.Marshal(map[string]any{"mode": contactsMode})
		result, exitCode, err := callAndClose(flagSocket, "contacts.sync", params)
		if err != nil {
			return exiterr(exitCode, err)
		}
		fmt.Println(formatResult("contacts.sync", result, flagJSON))
		return nil
	},
}

func init() {
	contactsLookupCmd.Flags().StringVar(&contactsJID, "jid", "", "contact JID")
	contactsSearchCmd.Flags().StringVar(&contactsQuery, "query", "", "trigram query")
	contactsSearchCmd.Flags().IntVar(&contactsLimit, "limit", 20, "max results (≤50)")
	contactsListCmd.Flags().IntVar(&contactsListLimit, "limit", 100, "max contacts (≤500)")
	contactsAnnotateCmd.Flags().StringVar(&contactsJID, "jid", "", "contact JID")
	contactsAnnotateCmd.Flags().StringVar(&contactsNotes, "notes", "", "free-text notes")
	contactsAnnotateCmd.Flags().StringSliceVar(&contactsTags, "tag", nil, "repeatable tag")
	contactsSyncCmd.Flags().StringVar(&contactsMode, "mode", "delta", "sync mode: delta | full")
	// #194: accept --chat as a universal recipient alias for --jid on the
	// contacts subcommands whose recipient flag is --jid.
	applyChatAlias(contactsLookupCmd, "jid")
	applyChatAlias(contactsAnnotateCmd, "jid")

	contactsCmd.AddCommand(contactsLookupCmd)
	contactsCmd.AddCommand(contactsSearchCmd)
	contactsCmd.AddCommand(contactsListCmd)
	contactsCmd.AddCommand(contactsAnnotateCmd)
	contactsCmd.AddCommand(contactsSyncCmd)
	rootCmd.AddCommand(contactsCmd)
}
