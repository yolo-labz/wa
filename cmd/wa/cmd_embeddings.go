package main

import (
	"encoding/json"
	"fmt"

	"github.com/spf13/cobra"
)

var embeddingsYes bool

var embeddingsCmd = &cobra.Command{
	Use:   "embeddings",
	Short: "Inspect and manage the vector index (requires embeddings feature flag)",
}

var embeddingsStatusCmd = &cobra.Command{
	Use:   "status",
	Short: "Report embedder/index state: enabled, model, dim, vector count",
	RunE: func(cmd *cobra.Command, args []string) error {
		result, exitCode, err := callAndClose(flagSocket, "embeddings.status", json.RawMessage(`{}`))
		if err != nil {
			return exiterr(exitCode, err)
		}
		if flagJSON {
			fmt.Println(formatResult("embeddings.status", result, flagJSON))
			return nil
		}
		var r struct {
			Enabled bool   `json:"enabled"`
			Model   string `json:"model,omitempty"`
			Dim     int    `json:"dim,omitempty"`
			Size    int    `json:"size"`
		}
		_ = json.Unmarshal(result, &r)
		fmt.Printf("enabled: %t\nmodel:   %s\ndim:     %d\nvectors: %d\n",
			r.Enabled, r.Model, r.Dim, r.Size)
		return nil
	},
}

var embeddingsPurgeCmd = &cobra.Command{
	Use:   "purge",
	Short: "Drop every vector from the index",
	RunE: func(cmd *cobra.Command, args []string) error {
		if !embeddingsYes {
			return exitf(64, "wa embeddings purge: --yes is required to confirm")
		}
		result, exitCode, err := callAndClose(flagSocket, "embeddings.purge", json.RawMessage(`{}`))
		if err != nil {
			return exiterr(exitCode, err)
		}
		fmt.Println(formatResult("embeddings.purge", result, flagJSON))
		return nil
	},
}

func init() {
	embeddingsPurgeCmd.Flags().BoolVarP(&embeddingsYes, "yes", "y", false, "confirm vector purge")
	embeddingsCmd.AddCommand(embeddingsStatusCmd)
	embeddingsCmd.AddCommand(embeddingsPurgeCmd)
	rootCmd.AddCommand(embeddingsCmd)
}
