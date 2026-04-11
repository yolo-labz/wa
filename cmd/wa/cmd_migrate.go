package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/spf13/cobra"
)

// migrateCmd — `wa migrate [--dry-run|--rollback]`.
//
// This is a thin client-side wrapper that delegates the real work to
// `wad migrate` so all migration logic lives in one place (cmd/wad).
// The wad binary path is resolved via os.Executable() of wa with `wad`
// in the same directory, falling back to $PATH lookup.
//
// See FR-021, FR-022, and contracts/migration.md.
var (
	migrateDryRun   bool
	migrateRollback bool
)

var migrateCmd = &cobra.Command{
	Use:   "migrate",
	Short: "Migrate a pre-008 single-profile install to the 008 per-profile layout",
	Long: `Migrate a pre-008 (007-format) wa install to the 008 per-profile layout.

The migration is normally performed automatically on first run of a wad
daemon built from feature 008. This subcommand exposes the same
transaction explicitly so operators can preview (--dry-run) or reverse
(--rollback) the migration.

Exit codes:
  0   success
  10  daemon busy (flock held) or rollback failed pre-condition
  64  usage error
  78  pre-flight failure (EXDEV, ownership, free space, invalid state)`,
	RunE: func(cmd *cobra.Command, args []string) error {
		if migrateDryRun && migrateRollback {
			return fmt.Errorf("--dry-run and --rollback are mutually exclusive")
		}

		wadPath, err := resolveWadBinary()
		if err != nil {
			return fmt.Errorf("locate wad binary: %w", err)
		}

		// Build `wad migrate` argv.
		wadArgs := []string{"migrate"}
		if migrateDryRun {
			wadArgs = append(wadArgs, "--dry-run")
		}
		if migrateRollback {
			wadArgs = append(wadArgs, "--rollback")
		}

		// Forward profile flag if set on the root command.
		if flagProfile != "" {
			wadArgs = append(wadArgs, "--profile", flagProfile)
		}

		child := exec.Command(wadPath, wadArgs...) //nolint:gosec // wadArgs is argv, not a shell string (FR-049)
		child.Stdout = os.Stdout
		child.Stderr = os.Stderr
		child.Stdin = os.Stdin

		if err := child.Run(); err != nil {
			if exitErr, ok := err.(*exec.ExitError); ok {
				os.Exit(exitErr.ExitCode())
			}
			return err
		}
		return nil
	},
}

// resolveWadBinary returns the absolute path to the wad binary. Tries
// (1) same directory as wa, (2) PATH lookup.
func resolveWadBinary() (string, error) {
	waPath, err := os.Executable()
	if err == nil {
		candidate := filepath.Join(filepath.Dir(waPath), "wad")
		if _, err := os.Stat(candidate); err == nil {
			return candidate, nil
		}
	}
	return exec.LookPath("wad")
}

func init() {
	migrateCmd.Flags().BoolVar(&migrateDryRun, "dry-run", false, "print planned moves without acting")
	migrateCmd.Flags().BoolVar(&migrateRollback, "rollback", false, "reverse a completed 008 migration")
	rootCmd.AddCommand(migrateCmd)
}
