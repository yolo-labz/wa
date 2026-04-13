package main

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

var (
	searchQuery string
	searchLimit int
)

var searchCmd = &cobra.Command{
	Use:   "search",
	Short: "Full-text search across all messages",
	RunE: func(cmd *cobra.Command, args []string) error {
		if searchQuery == "" {
			fmt.Fprintln(os.Stderr, "wa search: --query is required")
			os.Exit(64)
		}
		params, _ := json.Marshal(map[string]any{"query": searchQuery, "limit": searchLimit})
		result, exitCode, err := callAndClose(flagSocket, "search", params)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(exitCode)
		}
		if flagJSON {
			printNDJSON("wa.search/v1", result)
			return nil
		}
		printMessageTable(result)
		return nil
	},
}

func init() {
	searchCmd.Flags().StringVar(&searchQuery, "query", "", "FTS5 search query")
	searchCmd.Flags().IntVar(&searchLimit, "limit", 20, "max results")
}
