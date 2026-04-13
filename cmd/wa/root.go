package main

import (
	"github.com/spf13/cobra"
)

// Global flags set by persistent flags on the root command.
var (
	flagSocket  string
	flagJSON    bool
	flagVerbose bool
	flagProfile string // feature 008: --profile global flag
)

// resolvedProfileName holds the profile selected by the precedence chain,
// populated by rootCmd.PersistentPreRunE. Subcommands that need the profile
// name (e.g. profile use/rm) read this rather than calling ResolveProfile
// again.
var resolvedProfileName string

// rootCmd is the wa CLI root command.
var rootCmd = &cobra.Command{
	Use:   "wa",
	Short: "WhatsApp automation CLI",
	Long:  "wa is a thin JSON-RPC client for the wad daemon.",
	// Silence cobra's default usage/error printing so we control output.
	SilenceUsage:  true,
	SilenceErrors: true,

	// PersistentPreRunE resolves the active profile from the precedence
	// chain (FR-001) BEFORE any subcommand runs. This ensures flagSocket
	// is overridden with the per-profile path when --profile is supplied.
	PersistentPreRunE: func(cmd *cobra.Command, _ []string) error {
		// Subcommands that don't need a profile (version, upgrade,
		// completion, migrate) bypass resolution by setting
		// Annotations["profile"]="skip".
		if cmd.Annotations["profile"] == "skip" {
			return nil
		}

		resolved, err := ResolveProfile(flagProfile)
		if err != nil {
			// Multi-profile ambiguity → exit 78 per FR-039.
			return exiterr(78, err)
		}
		resolvedProfileName = resolved.Name

		// If the user didn't explicitly set --socket, derive it from the
		// resolved profile. This is the wire-up that makes `wa --profile
		// work status` query the work daemon.
		if !cmd.Flags().Changed("socket") {
			if sockPath := socketPathForProfile(resolved.Name); sockPath != "" {
				flagSocket = sockPath
			}
		}
		return nil
	},
}

func init() {
	// Resolve default socket path (best-effort; error surfaced at dial time).
	// After feature 008 the active socket is determined per-invocation by
	// PersistentPreRunE via socketPathForProfile() once the profile is
	// resolved. We use the `default` profile's socket as a fallback for
	// the narrow window before PreRunE fires.
	defaultSocket := socketPathForProfile(DefaultProfile)

	rootCmd.PersistentFlags().StringVar(&flagSocket, "socket", defaultSocket, "path to wad unix socket")
	rootCmd.PersistentFlags().BoolVar(&flagJSON, "json", false, "output NDJSON instead of human-readable text")
	rootCmd.PersistentFlags().BoolVar(&flagVerbose, "verbose", false, "enable verbose output")
	rootCmd.PersistentFlags().StringVar(&flagProfile, "profile", "", "profile name (see 'wa profile list')")
	// Shell completion for --profile globally. Ignore registration errors
	// (they only fire if the flag is already registered, which it isn't).
	_ = rootCmd.RegisterFlagCompletionFunc("profile", completeProfileNames)

	// Register subcommands.
	rootCmd.AddCommand(pairCmd)
	rootCmd.AddCommand(sendCmd)
	rootCmd.AddCommand(statusCmd)
	rootCmd.AddCommand(allowCmd)
	rootCmd.AddCommand(panicCmd)
	rootCmd.AddCommand(groupsCmd)
	rootCmd.AddCommand(waitCmd)
	rootCmd.AddCommand(reactCmd)
	rootCmd.AddCommand(markReadCmd)
	rootCmd.AddCommand(sendMediaCmd)
	rootCmd.AddCommand(versionCmd)
	rootCmd.AddCommand(upgradeCmd)
	// Feature 009: history, messages, search, purge, export.
	rootCmd.AddCommand(historyCmd)
	rootCmd.AddCommand(messagesCmd)
	rootCmd.AddCommand(searchCmd)
	rootCmd.AddCommand(purgeCmd)
	rootCmd.AddCommand(exportCmd)
	// Feature 008 subcommands (added by their own init() functions in
	// cmd_profile.go, cmd_migrate.go). `profile` is annotated "skip" on
	// `create`/`rm`/`show` because they manipulate the profile tree
	// directly.
}
