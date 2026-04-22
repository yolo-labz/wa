package main

import (
	"encoding/json"
	"fmt"

	"github.com/spf13/cobra"
)

var (
	profileAvatarJID  string
	profileAvatarSize string
)

var profileAvatarCmd = &cobra.Command{
	Use:   "avatar",
	Short: "Fetch a contact's profile photo, content-addressed (FR-028)",
	Long: `Downloads the profile picture for --jid (preview by default,
--size full for the high-res version). The file lands under
$XDG_CACHE_HOME/wa/media/avatars/sha256/<aa>/<rest>.jpg and the absolute
path is printed. Re-running with the same (jid, size) returns the
cached path without re-downloading when the server reports no change.`,
	Annotations: map[string]string{"profile": "skip"},
	RunE: func(cmd *cobra.Command, args []string) error {
		if profileAvatarJID == "" {
			return exitf(64, "wa profile avatar: --jid is required")
		}
		params := map[string]any{"jid": profileAvatarJID}
		if profileAvatarSize != "" {
			params["size"] = profileAvatarSize
		}
		result, exitCode, err := callAndClose(flagSocket, "contacts.profilePhoto", params)
		if err != nil {
			return exiterr(exitCode, err)
		}
		if flagJSON {
			fmt.Println(formatResult("contacts.profilePhoto", result, true))
			return nil
		}
		var out struct {
			Path string `json:"path"`
		}
		if err := json.Unmarshal(result, &out); err != nil {
			return exiterr(70, fmt.Errorf("wa profile avatar: decode: %w", err))
		}
		fmt.Println(out.Path)
		return nil
	},
}

func init() {
	profileAvatarCmd.Flags().StringVar(&profileAvatarJID, "jid", "", "target JID (required)")
	profileAvatarCmd.Flags().StringVar(&profileAvatarSize, "size", "preview", "preview|full")
	profileCmd.AddCommand(profileAvatarCmd)
}
