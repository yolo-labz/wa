package main

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

var pairPhone string

var pairCmd = &cobra.Command{
	Use:   "pair",
	Short: "Pair with WhatsApp by scanning a QR code or entering a phone number",
	RunE: func(cmd *cobra.Command, args []string) error {
		params := map[string]any{}
		if pairPhone != "" {
			params["phone"] = pairPhone
		}

		result, exitCode, err := callAndClose(flagSocket, "pair", params)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(exitCode)
		}

		fmt.Println(formatResult("pair", result, flagJSON))
		return nil
	},
}

func init() {
	pairCmd.Flags().StringVar(&pairPhone, "phone", "", "E.164 phone number for phone-code pairing flow")
}
