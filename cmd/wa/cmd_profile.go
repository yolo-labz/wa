package main

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/adrg/xdg"
	"github.com/spf13/cobra"
)

// profileCmd is the `wa profile` subcommand group covering US3/US4/US6.
var profileCmd = &cobra.Command{
	Use:   "profile",
	Short: "Manage multi-profile state (list, use, create, rm, show)",
	Long: `Manage per-profile WhatsApp state.

Each profile is a named isolation boundary that scopes its own session
database, history, allowlist, audit log, and unix socket. Daemons run
one per profile.`,
	Annotations: map[string]string{"profile": "skip"},
}

// ---- profile list (US6, FR-025) -------------------------------------------

var profileListCmd = &cobra.Command{
	Use:         "list",
	Short:       "List profiles with status and JID",
	Annotations: map[string]string{"profile": "skip"},
	RunE: func(cmd *cobra.Command, args []string) error {
		return runProfileList(os.Stdout)
	},
}

// runProfileList writes the tabular profile list to w.
func runProfileList(w *os.File) error {
	// Read the raw directory entries so we can render `(invalid)` for
	// out-of-band names that violate the regex (FR-025, defensive
	// terminal-escape stripping).
	raw, err := listProfilesRaw()
	if err != nil {
		return fmt.Errorf("list profiles: %w", err)
	}
	sort.Strings(raw)

	// Read the active-profile pointer (if any).
	var activeName string
	if data, err := os.ReadFile(filepath.Join(xdg.ConfigHome, "wa", "active-profile")); err == nil { //nolint:gosec // path under config home
		activeName = strings.TrimSpace(strings.TrimPrefix(string(data), "\ufeff"))
	}

	// Header.
	if _, err := fmt.Fprintf(w, "%-20s  %-6s  %-14s  %-40s  %s\n",
		"PROFILE", "ACTIVE", "STATUS", "JID", "LAST_SEEN"); err != nil {
		return err
	}
	if _, err := fmt.Fprintf(w, "%-20s  %-6s  %-14s  %-40s  %s\n",
		strings.Repeat("-", 20),
		strings.Repeat("-", 6),
		strings.Repeat("-", 14),
		strings.Repeat("-", 40),
		strings.Repeat("-", 9),
	); err != nil {
		return err
	}

	if len(raw) == 0 {
		_, err := fmt.Fprintln(w, "(no profiles — run 'wa profile create default' or pair via 'wa pair')")
		return err
	}

	for _, name := range raw {
		safe, invalid := sanitizeProfileName(name)
		displayName := safe
		if invalid {
			displayName = safe + " (invalid)"
		}
		active := " "
		if name == activeName {
			active = "*"
		}

		status, jid, lastSeen := probeProfile(name)
		if _, err := fmt.Fprintf(w, "%-20s  %-6s  %-14s  %-40s  %s\n",
			displayName, active, status, jid, lastSeen); err != nil {
			return err
		}
	}
	return nil
}

// probeProfile returns (status, jid, lastSeen) for a profile name. For now
// it inspects filesystem state only; a full implementation would dial the
// socket and issue a `status` RPC.
func probeProfile(name string) (status, jid, lastSeen string) {
	if err := ValidateProfileName(name); err != nil {
		return "invalid", "", "-"
	}
	// Session DB present? (path is composed from validated profile name
	// and XDG base directory — G703 false positive)
	sessionDB := filepath.Join(xdg.DataHome, "wa", name, "session.db")
	if _, err := os.Stat(sessionDB); err != nil { //nolint:gosec // G703: path is under validated xdg.DataHome
		return "not-paired", "", "-"
	}
	// Socket present? (path is composed via socketPathForProfile — G703 false positive)
	sock := socketPathForProfile(name)
	fi, err := os.Stat(sock) //nolint:gosec // G703: path is composed from validated profile name
	if err != nil {
		return "daemon-stopped", "", "-"
	}
	// Best-effort last-seen — use the socket's mtime as a proxy.
	return "connected", "(unknown)", fi.ModTime().UTC().Format(time.RFC3339)
}

// ---- profile use (US4, FR-026) --------------------------------------------

var profileUseCmd = &cobra.Command{
	Use:         "use <name>",
	Short:       "Set the active profile (written to $XDG_CONFIG_HOME/wa/active-profile)",
	Args:        cobra.ExactArgs(1),
	Annotations: map[string]string{"profile": "skip"},
	RunE: func(cmd *cobra.Command, args []string) error {
		name := args[0]
		if err := ValidateProfileName(name); err != nil {
			return err
		}
		// Assert the profile exists on disk.
		sessionDB := filepath.Join(xdg.DataHome, "wa", name, "session.db")
		if _, err := os.Stat(sessionDB); err != nil {
			fmt.Fprintf(os.Stderr, "profile %q does not exist (no session.db at %s)\n", name, sessionDB)
			os.Exit(78)
		}
		// Atomic write: tempfile → rename.
		path := filepath.Join(xdg.ConfigHome, "wa", "active-profile")
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			return err
		}
		return atomicWriteClient(path, []byte(name+"\n"))
	},
}

// ---- profile create (US6, FR-027) -----------------------------------------

var profileCreateCmd = &cobra.Command{
	Use:         "create <name>",
	Short:       "Create a new profile directory tree (does NOT pair)",
	Args:        cobra.ExactArgs(1),
	Annotations: map[string]string{"profile": "skip"},
	RunE: func(cmd *cobra.Command, args []string) error {
		name := args[0]
		if err := ValidateProfileName(name); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(64)
		}
		// FR-027: case-insensitive collision check on APFS/HFS+.
		if err := checkCaseInsensitiveCollision(name); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(64)
		}
		// Create the per-profile tree.
		dirs := []string{
			filepath.Join(xdg.DataHome, "wa", name),
			filepath.Join(xdg.ConfigHome, "wa", name),
			filepath.Join(xdg.StateHome, "wa", name),
		}
		for _, d := range dirs {
			if err := os.MkdirAll(d, 0o700); err != nil {
				return fmt.Errorf("create %s: %w", d, err)
			}
		}
		// Seed empty allowlist.
		allowPath := filepath.Join(xdg.ConfigHome, "wa", name, "allowlist.toml")
		if _, err := os.Stat(allowPath); errors.Is(err, os.ErrNotExist) {
			if err := os.WriteFile(allowPath, []byte("# wa allowlist\n"), 0o600); err != nil {
				return fmt.Errorf("seed allowlist: %w", err)
			}
		}
		fmt.Printf("Created profile %q at %s\n", name, filepath.Join(xdg.DataHome, "wa", name))
		fmt.Printf("Run 'wa --profile %s pair' to pair a device.\n", name)
		return nil
	},
}

// checkCaseInsensitiveCollision returns an error if an existing profile
// differs only in case (APFS/HFS+ foot-gun). Pure case-folded compare.
func checkCaseInsensitiveCollision(name string) error {
	entries, err := os.ReadDir(filepath.Join(xdg.DataHome, "wa"))
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	lower := strings.ToLower(name)
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		if strings.ToLower(e.Name()) == lower && e.Name() != name {
			return fmt.Errorf("profile %q collides case-insensitively with existing %q — refusing",
				name, e.Name())
		}
	}
	return nil
}

// ---- profile rm (US6, FR-028) ---------------------------------------------

var profileRmYes bool

var profileRmCmd = &cobra.Command{
	Use:         "rm <name>",
	Short:       "Remove a profile (not active, not only, not running)",
	Args:        cobra.ExactArgs(1),
	Annotations: map[string]string{"profile": "skip"},
	RunE: func(cmd *cobra.Command, args []string) error {
		name := args[0]
		if err := ValidateProfileName(name); err != nil {
			return err
		}

		// Hard constraint 1: not the active profile.
		if data, err := os.ReadFile(filepath.Join(xdg.ConfigHome, "wa", "active-profile")); err == nil { //nolint:gosec // path under config home
			active := strings.TrimSpace(string(data))
			if active == name {
				fmt.Fprintf(os.Stderr, "cannot remove active profile %q; switch first with 'wa profile use <other>'\n", name)
				os.Exit(78)
			}
		}
		// Hard constraint 2: not the only profile.
		profiles, err := enumerateProfiles()
		if err != nil {
			return err
		}
		if len(profiles) == 1 && profiles[0] == name {
			fmt.Fprintln(os.Stderr, "cannot remove the only profile")
			os.Exit(78)
		}
		// Hard constraint 3: no running daemon for this profile. A
		// stricter implementation would try to acquire the `.lock`
		// file via flock and fail if held. For now we only assert the
		// absence of the socket file itself, which is sufficient for
		// the common case where the daemon was stopped cleanly.
		sockPath := socketPathForProfile(name)
		if _, err := os.Stat(sockPath); err == nil { //nolint:gosec // path composed from validated profile name
			fmt.Fprintf(os.Stderr, "cannot remove profile %q: daemon appears to be running (socket exists at %s)\n", name, sockPath)
			os.Exit(78)
		}

		// Confirmation (unless --yes).
		if !profileRmYes {
			fmt.Fprintf(os.Stderr, "Remove profile %q and all its state? [y/N] ", name)
			reader := bufio.NewReader(os.Stdin)
			line, _ := reader.ReadString('\n')
			line = strings.TrimSpace(strings.ToLower(line))
			if line != "y" && line != "yes" {
				fmt.Fprintln(os.Stderr, "aborted")
				return nil
			}
		}

		// Remove the three per-profile directories.
		for _, root := range []string{xdg.DataHome, xdg.ConfigHome, xdg.StateHome} {
			target := filepath.Join(root, "wa", name)
			if err := os.RemoveAll(target); err != nil {
				return fmt.Errorf("remove %s: %w", target, err)
			}
		}
		fmt.Printf("Removed profile %q.\n", name)
		return nil
	},
}

// ---- profile show (US6) ---------------------------------------------------

var profileShowCmd = &cobra.Command{
	Use:         "show [name]",
	Short:       "Show metadata for a profile (defaults to active)",
	Args:        cobra.MaximumNArgs(1),
	Annotations: map[string]string{"profile": "skip"},
	RunE: func(cmd *cobra.Command, args []string) error {
		var name string
		if len(args) == 1 {
			name = args[0]
		} else {
			// Fall back to active profile.
			if data, err := os.ReadFile(filepath.Join(xdg.ConfigHome, "wa", "active-profile")); err == nil { //nolint:gosec // path under config home
				name = strings.TrimSpace(string(data))
			}
			if name == "" {
				name = DefaultProfile
			}
		}
		if err := ValidateProfileName(name); err != nil {
			return err
		}
		fmt.Printf("Profile: %s\n", name)
		fmt.Printf("  DataDir:       %s\n", filepath.Join(xdg.DataHome, "wa", name))
		fmt.Printf("  ConfigDir:     %s\n", filepath.Join(xdg.ConfigHome, "wa", name))
		fmt.Printf("  StateDir:      %s\n", filepath.Join(xdg.StateHome, "wa", name))
		fmt.Printf("  Socket:        %s\n", socketPathForProfile(name))
		status, jid, lastSeen := probeProfile(name)
		fmt.Printf("  Status:        %s\n", status)
		fmt.Printf("  JID:           %s\n", jid)
		fmt.Printf("  Last seen:     %s\n", lastSeen)
		return nil
	},
}

// ---- helpers --------------------------------------------------------------

// atomicWriteClient is the CLI-side equivalent of cmd/wad atomicWriteFile:
// tempfile → rename → fsync parent.
func atomicWriteClient(path string, content []byte) error {
	parent := filepath.Dir(path)
	if err := os.MkdirAll(parent, 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(parent, ".tmp-"+filepath.Base(path)+"-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer func() {
		if _, e := os.Stat(tmpName); e == nil {
			_ = os.Remove(tmpName)
		}
	}()
	if _, err := tmp.Write(content); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpName, path)
}

// completeProfileNames is the Cobra ValidArgsFunction for shell completion
// of profile names. Called by `wa --profile <TAB>` and `wa profile use
// <TAB>` / `wa profile rm <TAB>`.
func completeProfileNames(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
	names, err := enumerateProfiles()
	if err != nil {
		return nil, cobra.ShellCompDirectiveError
	}
	// Prefix-filter by toComplete.
	var out []string
	for _, n := range names {
		if strings.HasPrefix(n, toComplete) {
			out = append(out, n)
		}
	}
	return out, cobra.ShellCompDirectiveNoFileComp
}

func init() {
	profileRmCmd.Flags().BoolVarP(&profileRmYes, "yes", "y", false,
		"skip interactive confirmation (there is NO --force flag — constitution §III)")

	profileCmd.AddCommand(profileListCmd)
	profileCmd.AddCommand(profileUseCmd)
	profileCmd.AddCommand(profileCreateCmd)
	profileCmd.AddCommand(profileRmCmd)
	profileCmd.AddCommand(profileShowCmd)

	// Register completion functions for the use/rm subcommands.
	profileUseCmd.ValidArgsFunction = completeProfileNames
	profileRmCmd.ValidArgsFunction = completeProfileNames

	rootCmd.AddCommand(profileCmd)
}
