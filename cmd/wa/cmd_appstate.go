package main

import (
	"encoding/json"
	"fmt"

	"github.com/spf13/cobra"
)

var appstateCmd = &cobra.Command{
	Use:   "appstate",
	Short: "Inspect and repair WhatsApp app-state collections",
}

var (
	appstateCollection string
	appstateFull       bool
)

var appstateResyncCmd = &cobra.Command{
	Use:   "resync",
	Short: "Rebuild an app-state collection from the server",
	Long: `Discard the daemon's local app-state snapshot and refetch it.

Every app-state write — chat archive/mute/pin/markUnread, labels, and
"msg revoke --scope self" — sends a patch keyed on the daemon's LOCAL
version of a collection. If that version stops matching the server's, the
server answers 409 conflict and the catch-up fails to verify the returned
patches ("mismatching LTHash"). From then on every one of those methods
fails permanently, and before this command the only way out was
re-pairing the session.

--full (default true) is the repair: it throws away the local snapshot
and rebuilds. A catch-up cannot fix a hash mismatch, which is why repair
is the default; pass --full=false for the cheap incremental pull.

Without --collection every collection is resynced, and the result reports
each one separately rather than stopping at the first failure.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		params := map[string]any{"full": appstateFull}
		if appstateCollection != "" {
			params["collection"] = appstateCollection
		}
		result, exitCode, err := callAndClose(flagSocket, "appstate.resync", params)
		if err != nil {
			return exiterr(exitCode, err)
		}
		if flagJSON {
			fmt.Println(string(result))
			return nil
		}
		printAppStateResync(result)
		return nil
	},
}

// printAppStateResync renders one line per collection. A partial repair
// is the interesting case — the operator needs to see WHICH collection is
// still broken, not a single aggregate verdict.
func printAppStateResync(result json.RawMessage) {
	var r struct {
		Full    bool `json:"full"`
		Results []struct {
			Collection string `json:"collection"`
			OK         bool   `json:"ok"`
			Error      string `json:"error,omitempty"`
		} `json:"results"`
	}
	if err := json.Unmarshal(result, &r); err != nil {
		fmt.Println(string(result))
		return
	}
	mode := "full"
	if !r.Full {
		mode = "incremental"
	}
	fmt.Printf("app-state resync (%s)\n", mode)
	for _, x := range r.Results {
		if x.OK {
			fmt.Printf("  [OK  ] %s\n", x.Collection)
			continue
		}
		fmt.Printf("  [FAIL] %s — %s\n", x.Collection, x.Error)
	}
}

func init() {
	appstateResyncCmd.Flags().StringVar(&appstateCollection, "collection", "",
		"collection to resync (regular_high, regular_low, regular, critical_block, critical_unblock_low); default all")
	appstateResyncCmd.Flags().BoolVar(&appstateFull, "full", true,
		"discard the local snapshot and rebuild (default); --full=false does a cheap catch-up, which CANNOT repair a hash mismatch")
	appstateCmd.AddCommand(appstateResyncCmd)
	rootCmd.AddCommand(appstateCmd)
}
