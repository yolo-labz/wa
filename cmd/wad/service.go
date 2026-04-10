package main

import (
	"fmt"
	"os"
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
		if err := runInstallService(dryRun); err != nil {
			fmt.Fprintf(os.Stderr, "wad install-service: %v\n", err)
			os.Exit(1)
		}
		return true

	case "uninstall-service":
		if err := runUninstallService(); err != nil {
			fmt.Fprintf(os.Stderr, "wad uninstall-service: %v\n", err)
			os.Exit(1)
		}
		return true

	default:
		return false
	}
}

// runInstallService generates and installs the platform-specific service
// file. If dryRun is true, the generated file is printed to stdout instead.
func runInstallService(dryRun bool) error {
	// Refuse to run as root — services are user-level only.
	if os.Geteuid() == 0 {
		return fmt.Errorf("refusing to run as root; wad services are user-level only")
	}

	content, err := generateServiceFile()
	if err != nil {
		return fmt.Errorf("generate service file: %w", err)
	}

	if dryRun {
		fmt.Print(content)
		return nil
	}

	return installService(content)
}

// runUninstallService removes the platform-specific service file.
func runUninstallService() error {
	if os.Geteuid() == 0 {
		return fmt.Errorf("refusing to run as root; wad services are user-level only")
	}
	return uninstallService()
}

// hasBoolFlag checks os.Args for a boolean flag.
func hasBoolFlag(name string) bool {
	for _, arg := range os.Args[2:] {
		if arg == name {
			return true
		}
	}
	return false
}
