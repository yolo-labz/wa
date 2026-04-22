package main

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/spf13/cobra"
)

// groupCmd is the `wa group` parent for GroupAdmin subcommands (FR-020..FR-025).
// Separate from `wa groups` (listing/metadata) so adminish actions live under
// a singular noun and read operations stay plural — mirrors `wa chat` vs
// `wa chats`-style splits elsewhere in the CLI.
var groupCmd = &cobra.Command{
	Use:   "group",
	Short: "Administer WhatsApp groups (create, leave, roster, invite)",
}

// --- create ---------------------------------------------------------------

var (
	groupCreateSubject        string
	groupCreateParticipants   []string
	groupCreateIdempotencyKey string
)

var groupCreateCmd = &cobra.Command{
	Use:   "create",
	Short: "Create a new group (FR-020)",
	Long:  "Creates a group with --subject and --participant (repeatable). Adapter caps: ≤25 bytes subject, ≤5 groups/day.",
	RunE: func(cmd *cobra.Command, _ []string) error {
		if groupCreateSubject == "" {
			return exitf(64, "wa group create: --subject is required")
		}
		if len(groupCreateParticipants) == 0 {
			return exitf(64, "wa group create: at least one --participant is required")
		}
		params := map[string]any{
			"subject":      groupCreateSubject,
			"participants": groupCreateParticipants,
		}
		if groupCreateIdempotencyKey != "" {
			params["idempotencyKey"] = groupCreateIdempotencyKey
		}
		result, exitCode, err := callAndClose(flagSocket, "group.create", params)
		if err != nil {
			return exiterr(exitCode, err)
		}
		fmt.Println(formatResult("group.create", result, flagJSON))
		return nil
	},
}

// --- leave ----------------------------------------------------------------

var (
	groupLeaveJID            string
	groupLeaveIdempotencyKey string
)

var groupLeaveCmd = &cobra.Command{
	Use:   "leave",
	Short: "Leave a group (FR-023)",
	RunE: func(cmd *cobra.Command, _ []string) error {
		if groupLeaveJID == "" {
			return exitf(64, "wa group leave: --group is required")
		}
		params := map[string]any{"group": groupLeaveJID}
		if groupLeaveIdempotencyKey != "" {
			params["idempotencyKey"] = groupLeaveIdempotencyKey
		}
		result, exitCode, err := callAndClose(flagSocket, "group.leave", params)
		if err != nil {
			return exiterr(exitCode, err)
		}
		fmt.Println(formatResult("group.leave", result, flagJSON))
		return nil
	},
}

// --- roster (add/remove/promote/demote) -----------------------------------

// rosterCmdFlags holds the shared flag state for the four roster-patch
// commands. Declared once per command so cobra flag-binding sees distinct
// pointers and the -h/--help output stays scoped per subcommand.
type rosterCmdFlags struct {
	group          string
	participants   []string
	idempotencyKey string
}

var (
	groupAddFlags     rosterCmdFlags
	groupRemoveFlags  rosterCmdFlags
	groupPromoteFlags rosterCmdFlags
	groupDemoteFlags  rosterCmdFlags
)

func runRosterCmd(method string, f *rosterCmdFlags) error {
	if f.group == "" {
		return exitf(64, "wa group %s: --group is required", strings.TrimPrefix(method, "group."))
	}
	if len(f.participants) == 0 {
		return exitf(64, "wa group %s: at least one --participant is required", strings.TrimPrefix(method, "group."))
	}
	params := map[string]any{
		"group":        f.group,
		"participants": f.participants,
	}
	if f.idempotencyKey != "" {
		params["idempotencyKey"] = f.idempotencyKey
	}
	result, exitCode, err := callAndClose(flagSocket, method, params)
	if err != nil {
		return exiterr(exitCode, err)
	}
	fmt.Println(formatResult(method, result, flagJSON))
	return nil
}

var groupAddCmd = &cobra.Command{
	Use:   "add",
	Short: "Add participants to a group (FR-021)",
	Long:  "Adds --participant (repeatable) to --group. Adapter cap: ≤50 adds/day.",
	RunE: func(cmd *cobra.Command, _ []string) error {
		return runRosterCmd("group.addParticipants", &groupAddFlags)
	},
}

var groupRemoveCmd = &cobra.Command{
	Use:   "remove",
	Short: "Remove participants from a group (FR-022)",
	RunE: func(cmd *cobra.Command, _ []string) error {
		return runRosterCmd("group.removeParticipants", &groupRemoveFlags)
	},
}

var groupPromoteCmd = &cobra.Command{
	Use:   "promote",
	Short: "Promote participants to admin (FR-024)",
	RunE: func(cmd *cobra.Command, _ []string) error {
		return runRosterCmd("group.promote", &groupPromoteFlags)
	},
}

var groupDemoteCmd = &cobra.Command{
	Use:   "demote",
	Short: "Demote participants from admin (FR-024)",
	RunE: func(cmd *cobra.Command, _ []string) error {
		return runRosterCmd("group.demote", &groupDemoteFlags)
	},
}

// --- edit -----------------------------------------------------------------

var (
	groupEditJID            string
	groupEditSubject        string
	groupEditDescription    string
	groupEditIconPath       string
	groupEditRemoveIcon     bool
	groupEditIdempotencyKey string
)

var groupEditCmd = &cobra.Command{
	Use:   "edit",
	Short: "Edit group metadata (subject/description/icon) (FR-024)",
	Long: `Applies the supplied fields in order (subject → description → icon). At
least one field must be provided. --remove-icon clears the current icon;
--icon-path sets a new one from a JPEG on disk.`,
	RunE: func(cmd *cobra.Command, _ []string) error {
		if groupEditJID == "" {
			return exitf(64, "wa group edit: --group is required")
		}
		if groupEditIconPath != "" && groupEditRemoveIcon {
			return exitf(64, "wa group edit: --icon-path and --remove-icon are mutually exclusive")
		}
		params := map[string]any{"group": groupEditJID}
		if cmd.Flags().Changed("subject") {
			params["subject"] = groupEditSubject
		}
		if cmd.Flags().Changed("description") {
			params["description"] = groupEditDescription
		}
		if groupEditRemoveIcon {
			params["iconPath"] = ""
		} else if groupEditIconPath != "" {
			params["iconPath"] = groupEditIconPath
		}
		// Require at least one patch field beyond "group".
		if len(params) == 1 {
			return exitf(64, "wa group edit: provide --subject, --description, --icon-path, or --remove-icon")
		}
		if groupEditIdempotencyKey != "" {
			params["idempotencyKey"] = groupEditIdempotencyKey
		}
		result, exitCode, err := callAndClose(flagSocket, "group.edit", params)
		if err != nil {
			return exiterr(exitCode, err)
		}
		fmt.Println(formatResult("group.edit", result, flagJSON))
		return nil
	},
}

// --- invite (get/revoke/join) ---------------------------------------------

var groupInviteCmd = &cobra.Command{
	Use:   "invite",
	Short: "Manage group invite URLs (FR-025)",
}

var (
	groupInviteGetJID    string
	groupInviteRevokeJID string
	groupInviteRevokeKey string
	groupInviteJoinURL   string
	groupInviteJoinKey   string
)

func printInviteLink(method string, raw json.RawMessage) {
	if flagJSON {
		fmt.Println(formatResult(method, raw, true))
		return
	}
	var link struct {
		Group string `json:"group"`
		URL   string `json:"url"`
		Code  string `json:"code"`
	}
	if err := json.Unmarshal(raw, &link); err != nil {
		fmt.Println(formatResult(method, raw, false))
		return
	}
	fmt.Println(link.URL)
}

var groupInviteGetCmd = &cobra.Command{
	Use:   "get",
	Short: "Get the current invite URL (FR-025)",
	RunE: func(cmd *cobra.Command, _ []string) error {
		if groupInviteGetJID == "" {
			return exitf(64, "wa group invite get: --group is required")
		}
		result, exitCode, err := callAndClose(flagSocket, "group.inviteGet", map[string]any{"group": groupInviteGetJID})
		if err != nil {
			return exiterr(exitCode, err)
		}
		printInviteLink("group.inviteGet", result)
		return nil
	},
}

var groupInviteRevokeCmd = &cobra.Command{
	Use:   "revoke",
	Short: "Revoke the current invite URL and return a fresh one (FR-025)",
	RunE: func(cmd *cobra.Command, _ []string) error {
		if groupInviteRevokeJID == "" {
			return exitf(64, "wa group invite revoke: --group is required")
		}
		params := map[string]any{"group": groupInviteRevokeJID}
		if groupInviteRevokeKey != "" {
			params["idempotencyKey"] = groupInviteRevokeKey
		}
		result, exitCode, err := callAndClose(flagSocket, "group.inviteRevoke", params)
		if err != nil {
			return exiterr(exitCode, err)
		}
		printInviteLink("group.inviteRevoke", result)
		return nil
	},
}

var groupInviteJoinCmd = &cobra.Command{
	Use:   "join",
	Short: "Join a group via invite URL (FR-025)",
	Long:  "URL must match ^https://chat.whatsapp.com/[A-Za-z0-9]+$. Revoked/invalid links return -32000 upstream_error.",
	RunE: func(cmd *cobra.Command, _ []string) error {
		if groupInviteJoinURL == "" {
			return exitf(64, "wa group invite join: --url is required")
		}
		params := map[string]any{"url": groupInviteJoinURL}
		if groupInviteJoinKey != "" {
			params["idempotencyKey"] = groupInviteJoinKey
		}
		result, exitCode, err := callAndClose(flagSocket, "group.inviteJoin", params)
		if err != nil {
			return exiterr(exitCode, err)
		}
		fmt.Println(formatResult("group.inviteJoin", result, flagJSON))
		return nil
	},
}

func init() {
	// create
	groupCreateCmd.Flags().StringVar(&groupCreateSubject, "subject", "", "group subject (≤25 bytes)")
	groupCreateCmd.Flags().StringSliceVar(&groupCreateParticipants, "participant", nil, "participant JID (repeatable)")
	groupCreateCmd.Flags().StringVar(&groupCreateIdempotencyKey, "idempotency-key", "", "FR-034a replay key")

	// leave
	groupLeaveCmd.Flags().StringVar(&groupLeaveJID, "group", "", "group JID")
	groupLeaveCmd.Flags().StringVar(&groupLeaveIdempotencyKey, "idempotency-key", "", "FR-034a replay key")

	// roster shared flag binding
	bindRosterFlags := func(cmd *cobra.Command, f *rosterCmdFlags) {
		cmd.Flags().StringVar(&f.group, "group", "", "group JID")
		cmd.Flags().StringSliceVar(&f.participants, "participant", nil, "participant JID (repeatable)")
		cmd.Flags().StringVar(&f.idempotencyKey, "idempotency-key", "", "FR-034a replay key")
	}
	bindRosterFlags(groupAddCmd, &groupAddFlags)
	bindRosterFlags(groupRemoveCmd, &groupRemoveFlags)
	bindRosterFlags(groupPromoteCmd, &groupPromoteFlags)
	bindRosterFlags(groupDemoteCmd, &groupDemoteFlags)

	// edit
	groupEditCmd.Flags().StringVar(&groupEditJID, "group", "", "group JID")
	groupEditCmd.Flags().StringVar(&groupEditSubject, "subject", "", "new subject")
	groupEditCmd.Flags().StringVar(&groupEditDescription, "description", "", "new description")
	groupEditCmd.Flags().StringVar(&groupEditIconPath, "icon-path", "", "path to JPEG for new icon")
	groupEditCmd.Flags().BoolVar(&groupEditRemoveIcon, "remove-icon", false, "remove the current icon")
	groupEditCmd.Flags().StringVar(&groupEditIdempotencyKey, "idempotency-key", "", "FR-034a replay key")

	// invite get/revoke/join
	groupInviteGetCmd.Flags().StringVar(&groupInviteGetJID, "group", "", "group JID")
	groupInviteRevokeCmd.Flags().StringVar(&groupInviteRevokeJID, "group", "", "group JID")
	groupInviteRevokeCmd.Flags().StringVar(&groupInviteRevokeKey, "idempotency-key", "", "FR-034a replay key")
	groupInviteJoinCmd.Flags().StringVar(&groupInviteJoinURL, "url", "", "invite URL (https://chat.whatsapp.com/...)")
	groupInviteJoinCmd.Flags().StringVar(&groupInviteJoinKey, "idempotency-key", "", "FR-034a replay key")
	groupInviteCmd.AddCommand(groupInviteGetCmd)
	groupInviteCmd.AddCommand(groupInviteRevokeCmd)
	groupInviteCmd.AddCommand(groupInviteJoinCmd)

	// Wire subcommands onto the group parent.
	groupCmd.AddCommand(groupCreateCmd)
	groupCmd.AddCommand(groupLeaveCmd)
	groupCmd.AddCommand(groupAddCmd)
	groupCmd.AddCommand(groupRemoveCmd)
	groupCmd.AddCommand(groupPromoteCmd)
	groupCmd.AddCommand(groupDemoteCmd)
	groupCmd.AddCommand(groupEditCmd)
	groupCmd.AddCommand(groupInviteCmd)
	rootCmd.AddCommand(groupCmd)
}
