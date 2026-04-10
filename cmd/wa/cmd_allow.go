package main

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"
)

var allowActions string

var allowCmd = &cobra.Command{
	Use:   "allow",
	Short: "Manage the daemon's JID allowlist",
}

var allowAddCmd = &cobra.Command{
	Use:   "add <jid>",
	Short: "Grant actions to a JID",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		if allowActions == "" {
			fmt.Fprintln(os.Stderr, "wa allow add: --actions is required")
			os.Exit(64)
		}

		params := map[string]any{
			"op":      "add",
			"jid":     args[0],
			"actions": strings.Split(allowActions, ","),
		}

		result, exitCode, err := callAndClose(flagSocket, "allow", params)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(exitCode)
		}

		fmt.Println(formatResult("allow", result, flagJSON))
		return nil
	},
}

var allowRemoveCmd = &cobra.Command{
	Use:   "remove <jid>",
	Short: "Revoke all actions from a JID",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		params := map[string]any{
			"op":  "remove",
			"jid": args[0],
		}

		result, exitCode, err := callAndClose(flagSocket, "allow", params)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(exitCode)
		}

		fmt.Println(formatResult("allow", result, flagJSON))
		return nil
	},
}

var allowListCmd = &cobra.Command{
	Use:   "list",
	Short: "List all allowlist entries",
	RunE: func(cmd *cobra.Command, args []string) error {
		params := map[string]any{
			"op": "list",
		}

		result, exitCode, err := callAndClose(flagSocket, "allow", params)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(exitCode)
		}

		if flagJSON {
			fmt.Println(formatJSON("allow", result))
			return nil
		}

		// Human-readable tabular output.
		var resp struct {
			Rules []struct {
				JID     string   `json:"jid"`
				Actions []string `json:"actions"`
			} `json:"rules"`
		}
		if err := json.Unmarshal(result, &resp); err != nil {
			fmt.Println(string(result))
			return nil
		}

		if len(resp.Rules) == 0 {
			fmt.Println("No allowlist entries (default deny)")
			return nil
		}

		w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
		_, _ = fmt.Fprintln(w, "JID\tACTIONS")
		for _, r := range resp.Rules {
			_, _ = fmt.Fprintf(w, "%s\t%s\n", r.JID, strings.Join(r.Actions, ","))
		}
		_ = w.Flush()
		return nil
	},
}

func init() {
	allowAddCmd.Flags().StringVar(&allowActions, "actions", "", "comma-separated actions: send,read,group.add,group.create")
	allowCmd.AddCommand(allowAddCmd)
	allowCmd.AddCommand(allowRemoveCmd)
	allowCmd.AddCommand(allowListCmd)
}
