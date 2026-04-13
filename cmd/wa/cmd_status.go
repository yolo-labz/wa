package main

import (
	"fmt"

	"github.com/spf13/cobra"
)

var statusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show daemon connection status",
	RunE: func(cmd *cobra.Command, args []string) error {
		result, exitCode, err := callAndClose(flagSocket, "status", nil)
		if err != nil {
			return exiterr(exitCode, err)
		}

		fmt.Println(formatResult("status", result, flagJSON))
		return nil
	},
}
