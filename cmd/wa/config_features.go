package main

import (
	"encoding/json"
	"fmt"
	"os"
	"text/tabwriter"

	"github.com/spf13/cobra"
)

// configCmd is the parent for per-profile config introspection.
var configCmd = &cobra.Command{
	Use:   "config",
	Short: "Inspect resolved daemon configuration",
}

var configFeaturesCmd = &cobra.Command{
	Use:   "features",
	Short: "Show resolved feature flags (FR-100 / FR-114 / FR-122)",
	RunE: func(cmd *cobra.Command, args []string) error {
		result, exitCode, err := callAndClose(flagSocket, "config.features", nil)
		if err != nil {
			return exiterr(exitCode, err)
		}
		if flagJSON {
			fmt.Println(formatJSON("config.features", result))
			return nil
		}
		var f struct {
			Embeddings     bool `json:"embeddings"`
			ScheduledSends bool `json:"scheduledSends"`
			Labels         bool `json:"labels"`
		}
		if err := json.Unmarshal(result, &f); err != nil {
			fmt.Println(formatHuman("config.features", result))
			return nil
		}
		w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
		_, _ = fmt.Fprintln(w, "FEATURE\tSTATE")
		_, _ = fmt.Fprintf(w, "embeddings\t%s\n", onOff(f.Embeddings))
		_, _ = fmt.Fprintf(w, "scheduled_sends\t%s\n", onOff(f.ScheduledSends))
		_, _ = fmt.Fprintf(w, "labels\t%s\n", onOff(f.Labels))
		_ = w.Flush()
		return nil
	},
}

func onOff(b bool) string {
	if b {
		return "on"
	}
	return "off"
}

func init() {
	configCmd.AddCommand(configFeaturesCmd)
}
