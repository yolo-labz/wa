package main

import (
	"fmt"

	"github.com/spf13/cobra"
)

var panicCmd = &cobra.Command{
	Use:   "panic",
	Short: "Unlink device server-side and wipe local session",
	Long: `Panic unlinks the device from WhatsApp servers and wipes the local
session store. The next "wa pair" will start a fresh QR flow.

This command requires no confirmation — the name is the warning.
It always succeeds locally even if the upstream unlink call fails.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		result, exitCode, err := callAndClose(flagSocket, "panic", nil)
		if err != nil {
			return exiterr(exitCode, err)
		}

		fmt.Println(formatResult("panic", result, flagJSON))
		return nil
	},
}
