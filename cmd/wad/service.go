package main

import (
	"fmt"
	"os"
	"slices"
)

// handleServiceCommand checks if os.Args[1] is a service management
// subcommand and handles it, returning true if it was handled.
// This keeps the daemon's main() path clean.
func handleServiceCommand() (handled bool) {
	if len(os.Args) < 2 {
		return false
	}

	switch os.Args[1] {
	case "install-service":
		dryRun := hasBoolFlag("--dry-run")
		profile := parseServiceProfileFlag()
		if err := runInstallService(dryRun, profile); err != nil {
			fmt.Fprintf(os.Stderr, "wad install-service: %v\n", err)
			os.Exit(1)
		}
		return true

	case "uninstall-service":
		profile := parseServiceProfileFlag()
		if err := runUninstallService(profile); err != nil {
			fmt.Fprintf(os.Stderr, "wad uninstall-service: %v\n", err)
			os.Exit(1)
		}
		return true

	case "migrate":
		// Feature 008: explicit migration subcommand.
		// Parses --dry-run / --rollback / --profile from the remaining args.
		exitCode := runMigrateSubcommand()
		os.Exit(exitCode)
		return true

	default:
		return false
	}
}

// parseServiceProfileFlag extracts --profile <name> (or --profile=<name>)
// from os.Args[2:]. Returns DefaultProfile if absent. This is a minimal
// parser because the daemon has no cobra on the install-service path.
func parseServiceProfileFlag() string {
	rest := os.Args[2:]
	for i := range rest {
		if rest[i] == "--profile" && i+1 < len(rest) {
			return rest[i+1]
		}
		if len(rest[i]) > len("--profile=") && rest[i][:len("--profile=")] == "--profile=" {
			return rest[i][len("--profile="):]
		}
	}
	return DefaultProfile
}

// runInstallService generates and installs the platform-specific service
// file for the given profile. If dryRun is true, the generated file is
// printed to stdout instead.
func runInstallService(dryRun bool, profile string) error {
	// Refuse to run as root (FR-038, feature 007 baseline preserved).
	if os.Geteuid() == 0 {
		return fmt.Errorf("refusing to run as root; wad services are user-level only")
	}

	if err := ValidateProfileName(profile); err != nil {
		return fmt.Errorf("profile %q: %w", profile, err)
	}

	content, err := generateServiceFileFor(profile)
	if err != nil {
		return fmt.Errorf("generate service file: %w", err)
	}

	if dryRun {
		fmt.Print(content)
		return nil
	}

	return installServiceFor(profile, content)
}

// runUninstallService removes the platform-specific service file for the
// given profile. Other profiles remain untouched per FR-036.
func runUninstallService(profile string) error {
	if os.Geteuid() == 0 {
		return fmt.Errorf("refusing to run as root; wad services are user-level only")
	}
	if err := ValidateProfileName(profile); err != nil {
		return fmt.Errorf("profile %q: %w", profile, err)
	}
	return uninstallServiceFor(profile)
}

// hasBoolFlag checks os.Args for a boolean flag.
func hasBoolFlag(name string) bool {
	return slices.Contains(os.Args[2:], name)
}
