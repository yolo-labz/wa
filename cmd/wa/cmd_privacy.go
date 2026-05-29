package main

import (
	"encoding/json"
	"fmt"

	"github.com/spf13/cobra"
)

// privacyCmd is the `wa privacy` parent for the privacy.get / privacy.set
// RPCs (FR-029/FR-030). Thin client: each subcommand makes one JSON-RPC
// call and prints the result.
var privacyCmd = &cobra.Command{
	Use:   "privacy",
	Short: "Read and change account privacy settings",
	Long: `privacy exposes the WhatsApp account privacy dimensions
(groups, readReceipts, lastSeen, profile, about) over the daemon.

  wa privacy get [--key <k>]            read the live server-side settings
  wa privacy set --key <k> --value <v>  change one setting

Keys and values are validated daemon-side; an unknown token comes back as
-32602 invalid_params (CLI exit 64).`,
}

var privacyGetKey string

var privacyGetCmd = &cobra.Command{
	Use:   "get",
	Short: "Print the live server-side privacy settings (FR-030)",
	Long: `get reads the privacy settings straight from the server — never a
local cache — so out-of-band changes made from the phone UI surface
immediately. Without --json it prints one "key: value" line per setting.

--key filters the output to a single dimension (client-side); the dimension
must be one of groups|readReceipts|lastSeen|profile|about.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		result, exitCode, err := callAndClose(flagSocket, "privacy.get", map[string]any{})
		if err != nil {
			return exiterr(exitCode, err)
		}
		return printPrivacyGet(result, privacyGetKey, flagJSON)
	},
}

var (
	privacySetKey   string
	privacySetValue string
)

var privacySetCmd = &cobra.Command{
	Use:   "set",
	Short: "Change one privacy setting (FR-029)",
	Long: `set changes a single privacy dimension to a new audience tier.

  --key    one of groups|readReceipts|lastSeen|profile|about
  --value  one of everyone|contacts|nobody

Both flags are required. The CLI passes the tokens through verbatim; the
daemon validates them and returns -32602 invalid_params (CLI exit 64) for
an unknown key or value.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		params := map[string]any{"key": privacySetKey, "value": privacySetValue}
		result, exitCode, err := callAndClose(flagSocket, "privacy.set", params)
		if err != nil {
			return exiterr(exitCode, err)
		}
		fmt.Println(formatResult("privacy.set", result, flagJSON))
		return nil
	},
}

// printPrivacyGet renders the privacy.get result. In --json mode it prints
// the schema-tagged NDJSON line for each setting (matching the
// printNDJSONField idiom neighbours use); otherwise it prints one
// "key: value" line per setting. When key != "" the output is filtered to
// that single dimension client-side (the daemon returns the full set).
func printPrivacyGet(result []byte, key string, asJSON bool) error {
	var out struct {
		Settings []struct {
			Key   string `json:"key"`
			Value string `json:"value"`
		} `json:"settings"`
	}
	if err := json.Unmarshal(result, &out); err != nil {
		return exiterr(70, fmt.Errorf("wa privacy get: decode: %w", err))
	}

	if asJSON {
		printNDJSONField("wa.privacy.get/v1", "settings", filterPrivacyJSON(result, key))
		return nil
	}

	printed := 0
	for _, s := range out.Settings {
		if key != "" && s.Key != key {
			continue
		}
		fmt.Printf("%s: %s\n", s.Key, s.Value)
		printed++
	}
	if printed == 0 {
		fmt.Println("(no privacy settings)")
	}
	return nil
}

// filterPrivacyJSON narrows the {settings:[...]} envelope to a single key
// so the --json path emits only the requested dimension. An empty key
// returns result unchanged.
func filterPrivacyJSON(result []byte, key string) json.RawMessage {
	if key == "" {
		return result
	}
	var env struct {
		Settings []json.RawMessage `json:"settings"`
	}
	if err := json.Unmarshal(result, &env); err != nil {
		return result
	}
	kept := make([]json.RawMessage, 0, len(env.Settings))
	for _, raw := range env.Settings {
		var t struct {
			Key string `json:"key"`
		}
		if err := json.Unmarshal(raw, &t); err == nil && t.Key == key {
			kept = append(kept, raw)
		}
	}
	filtered, err := json.Marshal(struct {
		Settings []json.RawMessage `json:"settings"`
	}{Settings: kept})
	if err != nil {
		return result
	}
	return filtered
}

func init() {
	privacyGetCmd.Flags().StringVar(&privacyGetKey, "key", "", "filter to one key (groups|readReceipts|lastSeen|profile|about)")

	privacySetCmd.Flags().StringVar(&privacySetKey, "key", "", "privacy key (groups|readReceipts|lastSeen|profile|about)")
	privacySetCmd.Flags().StringVar(&privacySetValue, "value", "", "audience tier (everyone|contacts|nobody)")
	_ = privacySetCmd.MarkFlagRequired("key")
	_ = privacySetCmd.MarkFlagRequired("value")

	privacyCmd.AddCommand(privacyGetCmd, privacySetCmd)
	rootCmd.AddCommand(privacyCmd)
}
