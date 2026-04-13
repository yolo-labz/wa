package main

import (
	"fmt"
	"strings"
	"time"

	"github.com/spf13/cobra"
)

var (
	waitEvents  string
	waitTimeout time.Duration
)

var waitCmd = &cobra.Command{
	Use:   "wait",
	Short: "Block until a matching event arrives from the daemon",
	RunE: func(cmd *cobra.Command, args []string) error {
		params := map[string]any{}
		if waitEvents != "" {
			params["events"] = strings.Split(waitEvents, ",")
		}
		if waitTimeout > 0 {
			params["timeoutMs"] = waitTimeout.Milliseconds()
		}

		result, exitCode, err := callAndClose(flagSocket, "wait", params)
		if err != nil {
			return exiterr(exitCode, err)
		}

		if flagJSON {
			fmt.Println(formatJSON("wait", result))
		} else {
			fmt.Println(string(result))
		}
		return nil
	},
}

func init() {
	waitCmd.Flags().StringVar(&waitEvents, "events", "", "comma-separated event types to match (e.g. message,receipt)")
	waitCmd.Flags().DurationVar(&waitTimeout, "timeout", 30*time.Second, "maximum time to wait")
}
