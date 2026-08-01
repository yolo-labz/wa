package main

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/spf13/cobra"
)

var healthCmd = &cobra.Command{
	Use:   "health",
	Short: "Non-blocking liveness probe (paired/connected/lastEventTs)",
	RunE: func(_ *cobra.Command, _ []string) error {
		result, exitCode, err := callAndClose(flagSocket, "health", nil)
		if err != nil {
			return exiterr(exitCode, err)
		}

		if flagJSON {
			fmt.Println(string(result))
			return nil
		}

		var h struct {
			Paired       bool   `json:"paired"`
			Connected    bool   `json:"connected"`
			Profile      string `json:"profile"`
			LastEventTs  int64  `json:"lastEventTs,omitempty"`
			SessionSince int64  `json:"sessionSince,omitempty"`
		}
		if err := json.Unmarshal(result, &h); err != nil {
			return exiterr(1, fmt.Errorf("decode health: %w", err))
		}

		fmt.Printf("profile:    %s\n", h.Profile)
		fmt.Printf("paired:     %t\n", h.Paired)
		fmt.Printf("connected:  %t\n", h.Connected)
		if h.LastEventTs > 0 {
			fmt.Printf("last-event: %s\n", time.Unix(h.LastEventTs, 0).UTC().Format(time.RFC3339))
		} else {
			fmt.Printf("last-event: -\n")
		}
		if h.SessionSince > 0 {
			fmt.Printf("session:    %s\n", time.Unix(h.SessionSince, 0).UTC().Format(time.RFC3339))
		} else {
			// Absent, not zero: a session paired before the daemon began
			// recording pairing times has no timestamp to report, and the
			// daemon omits the field rather than substituting one (issue
			// #311). Mirror last-event's dash so the row does not vanish.
			fmt.Printf("session:    -\n")
		}
		return nil
	},
}

func init() {
	rootCmd.AddCommand(healthCmd)
}
