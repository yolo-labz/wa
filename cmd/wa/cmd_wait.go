package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/spf13/cobra"
)

var (
	waitEvents  string
	waitTimeout time.Duration
	waitPollMax = 9500 * time.Millisecond // the daemon's per-call cap (app.MaxWaitMs)
)

var waitCmd = &cobra.Command{
	Use:   "wait",
	Short: "Block until a matching event arrives from the daemon",
	RunE: func(cmd *cobra.Command, args []string) error {
		params := map[string]any{}
		if waitEvents != "" {
			params["events"] = strings.Split(waitEvents, ",")
		}
		budget := waitTimeout
		if budget <= 0 {
			budget = 30 * time.Second
		}
		// A daemon wait is capped near 10 s (FR-116-16): re-poll in slices
		// until an event or the caller's deadline. ponytail: an event landing
		// between two polls is missed; a subscription would close that gap.
		deadline := time.Now().Add(budget)
		var result json.RawMessage
		for left := budget; ; left = time.Until(deadline) {
			params["timeoutMs"] = max(min(left, waitPollMax), time.Millisecond).Milliseconds()
			res, exitCode, err := callAndClose(flagSocket, "wait", params)
			result = res
			var re *rpcError
			if errors.As(err, &re) && waitTimedOut(re) && time.Until(deadline) > 0 {
				continue
			}
			if err != nil {
				return exiterr(exitCode, err)
			}
			break
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
