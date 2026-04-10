package main

import (
	"encoding/json"
	"fmt"
	"os"
	"text/tabwriter"

	"github.com/spf13/cobra"
)

var groupsCmd = &cobra.Command{
	Use:   "groups",
	Short: "List joined WhatsApp groups",
	RunE: func(cmd *cobra.Command, args []string) error {
		result, exitCode, err := callAndClose(flagSocket, "groups", nil)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(exitCode)
		}

		if flagJSON {
			fmt.Println(formatJSON("groups", result))
			return nil
		}

		// Human-readable tabular output.
		var resp struct {
			Groups []struct {
				JID          string `json:"jid"`
				Subject      string `json:"subject"`
				Participants []any  `json:"participants"`
			} `json:"groups"`
		}
		if err := json.Unmarshal(result, &resp); err != nil {
			fmt.Println(formatHuman("groups", result))
			return nil
		}

		if len(resp.Groups) == 0 {
			fmt.Println("No groups")
			return nil
		}

		w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
		_, _ = fmt.Fprintln(w, "JID\tSUBJECT\tMEMBERS")
		for _, g := range resp.Groups {
			_, _ = fmt.Fprintf(w, "%s\t%s\t%d\n", g.JID, g.Subject, len(g.Participants))
		}
		_ = w.Flush()
		return nil
	},
}
