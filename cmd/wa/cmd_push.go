package main

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

// pushCmd uploads a client-local file to the --remote daemon's
// content-addressed media store and prints the sha256 the daemon computed.
// It is the staging primitive behind `sendMedia --remote`: upload once, then
// reference the returned hash from any number of `sendMedia --sha256` /
// `media fetch` calls without re-transferring the bytes. Spec 198.
//
// Upload is a --remote-only operation: in local/socket mode the daemon can
// already read the operator's filesystem directly, so there is nothing to
// upload — the command refuses with a clear usage error rather than silently
// no-opping.
var pushCmd = &cobra.Command{
	Use:   "push <file>",
	Short: "Upload a local file to a --remote daemon's media store; prints its sha256",
	Long: `Upload a client-local file to a --remote daemon's content-addressed media
store and print the resulting sha256. Stage a file once, then reference the
hash from sendMedia --sha256 or media fetch without re-uploading.

  wa --remote https://wa.example.com push ./poster.png
  wa --remote https://wa.example.com sendMedia --to <jid> --sha256 <printed-hash>

Authentication uses the $WA_TOKEN environment variable (never a flag, so the
token does not leak through argv). Requires the 'send' token scope.`,
	Args: cobra.ExactArgs(1),
	RunE: func(_ *cobra.Command, args []string) error {
		if flagRemote == "" {
			return exitf(64, "wa push: --remote is required (in local mode the daemon already reads your filesystem; nothing to upload)")
		}
		payload, err := readLocalMediaForUpload(args[0])
		if err != nil {
			return err
		}
		sha, size, exitCode, err := callRemoteUpload(flagRemote, os.Getenv("WA_TOKEN"), payload)
		if err != nil {
			return exiterr(exitCode, err)
		}
		if flagJSON {
			out, _ := json.Marshal(map[string]any{
				"schema": "wa.media.upload/v1",
				"sha256": sha,
				"size":   size,
			})
			fmt.Println(string(out))
			return nil
		}
		// Plain mode: print just the sha on stdout so it pipes cleanly into a
		// subsequent `sendMedia --sha256 "$(wa push ...)"`.
		fmt.Println(sha)
		return nil
	},
}

func init() {
	rootCmd.AddCommand(pushCmd)
}
