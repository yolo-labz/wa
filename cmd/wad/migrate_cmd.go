// Package main — explicit `wad migrate` subcommand handling.
package main

import (
	"errors"
	"fmt"
	"log/slog"
	"os"
)

// runMigrateSubcommand handles `wad migrate [--dry-run|--rollback]`.
// Returns an exit code; caller os.Exits with it.
//
// Exit codes per contracts/migration.md §Error handling:
//
//	0    success / already migrated
//	1    unexpected runtime error
//	10   flock / DB open pre-condition failed
//	64   usage (mutually-exclusive flags)
//	78   migration pre-flight failed (EXDEV, ownership, free space)
func runMigrateSubcommand() int {
	// Parse flags from os.Args[2:] (os.Args[1] is "migrate").
	var (
		dryRun   bool
		rollback bool
		profile  = DefaultProfile
	)
	rest := os.Args[2:]
	for i := 0; i < len(rest); i++ {
		switch rest[i] {
		case "--dry-run":
			dryRun = true
		case "--rollback":
			rollback = true
		case "--profile":
			if i+1 >= len(rest) {
				fmt.Fprintln(os.Stderr, "wad migrate: --profile requires a value")
				return 64
			}
			profile = rest[i+1]
			i++
		default:
			if len(rest[i]) > len("--profile=") && rest[i][:len("--profile=")] == "--profile=" {
				profile = rest[i][len("--profile="):]
				continue
			}
			fmt.Fprintf(os.Stderr, "wad migrate: unknown argument %q\n", rest[i])
			return 64
		}
	}

	if dryRun && rollback {
		fmt.Fprintln(os.Stderr, "wad migrate: --dry-run and --rollback are mutually exclusive")
		return 64
	}

	resolver, err := NewPathResolver(profile)
	if err != nil {
		fmt.Fprintf(os.Stderr, "wad migrate: profile %q: %v\n", profile, err)
		return 64
	}

	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))

	tx := &MigrationTx{Logger: log, Resolver: resolver, DryRun: dryRun, Rollback: rollback}

	switch {
	case rollback:
		if err := tx.ApplyRollback(); err != nil {
			fmt.Fprintf(os.Stderr, "wad migrate --rollback: %v\n", err)
			return classifyMigrateError(err)
		}
		fmt.Println("rollback complete")
		return 0

	case dryRun:
		plan, err := tx.Plan()
		if err != nil {
			fmt.Fprintf(os.Stderr, "wad migrate --dry-run: %v\n", err)
			return classifyMigrateError(err)
		}
		printMigrationPlan(plan, resolver)
		return 0

	default:
		// Idempotent forward migration.
		if err := autoMigrate(resolver, log); err != nil {
			fmt.Fprintf(os.Stderr, "wad migrate: %v\n", err)
			return classifyMigrateError(err)
		}
		fmt.Println("migration complete")
		return 0
	}
}

// classifyMigrateError maps migration errors to exit codes per
// contracts/migration.md §Error handling.
func classifyMigrateError(err error) int {
	switch {
	case errors.Is(err, ErrCrossFilesystem):
		return 78
	case errors.Is(err, ErrMigrationAborted):
		return 78
	default:
		return 1
	}
}

// printMigrationPlan renders a dry-run plan in the format documented in
// contracts/migration.md §Dry-run output format.
func printMigrationPlan(plan []MigrationStep, r *PathResolver) {
	fmt.Println("Migration plan (schema v1 → v2):")
	fmt.Println()
	fmt.Println("Pre-flight:")
	fmt.Println("  Cross-filesystem check:      OK")
	fmt.Println("  SQLite WAL checkpoint:       will run before staging")
	fmt.Println("  Staging directory:           ", r.DataDir())
	fmt.Println()
	fmt.Printf("%-42s  %s\n", "FROM", "TO")
	fmt.Printf("%-42s  %s\n", "----------------------------------------", "----------------------------------------")
	for _, step := range plan {
		if step.Kind != "copy" {
			continue
		}
		fmt.Printf("%-42s  %s\n", step.From, step.To)
	}
	fmt.Println()
	fmt.Println("After migration:")
	fmt.Printf("  Schema version:  %d\n", SchemaVersion)
	fmt.Printf("  Active profile:  %s\n", DefaultProfile)
	fmt.Println()
	fmt.Println("Run 'wad migrate' (without --dry-run) to apply.")
}
