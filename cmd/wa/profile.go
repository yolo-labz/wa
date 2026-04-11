// Package main — feature 008 profile resolution and enumeration for the wa CLI.
//
// The client side of profile handling: resolve the active profile from
// the FR-001 precedence chain (flag > env > active-profile file > default),
// enumerate profiles from the filesystem, and query each for status.
//
// Profile name validation (the ValidateProfileName regex and reserved
// list) lives in cmd/wad/profile.go. The CLI imports nothing from cmd/wad
// directly, so this file contains a minimal validator copy. If a future
// refactor moves validation to an internal package, both copies should
// be replaced with a shared import.
package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/adrg/xdg"
)

// ErrInvalidProfileName is returned by validation failures on the CLI side.
var ErrInvalidProfileName = errors.New("invalid profile name")

// ErrReservedProfileName is returned when a name is in the reserved list.
var ErrReservedProfileName = errors.New("reserved profile name")

// ErrMultipleProfiles is returned when no profile hint is supplied but
// multiple profiles exist (FR-039, exit code 78).
var ErrMultipleProfiles = errors.New("multiple profiles exist; pass --profile or run 'wa profile use <name>'")

// DefaultProfile is the canonical default profile name.
const DefaultProfile = "default"

// profileNameRegex matches the FR-002 regex.
var profileNameRegex = regexp.MustCompile(`^[a-z][a-z0-9-]{0,30}[a-z0-9]$`)

// reservedProfileNames is the case-folded set of rejected names.
// This MUST stay in sync with cmd/wad/profile.go.
var reservedProfileNames = map[string]struct{}{
	".": {}, "..": {},
	"con": {}, "prn": {}, "aux": {}, "nul": {},
	"com0": {}, "com1": {}, "com2": {}, "com3": {}, "com4": {},
	"com5": {}, "com6": {}, "com7": {}, "com8": {}, "com9": {},
	"lpt0": {}, "lpt1": {}, "lpt2": {}, "lpt3": {}, "lpt4": {},
	"lpt5": {}, "lpt6": {}, "lpt7": {}, "lpt8": {}, "lpt9": {},
	"conin$": {}, "conout$": {},
	"wa": {}, "wad": {}, "root": {}, "system": {},
	"dbus": {}, "systemd": {}, "user": {}, "session": {},
	"list": {}, "use": {}, "create": {}, "rm": {}, "show": {},
	"new": {}, "delete": {}, "current": {}, "switch": {},
	"all": {}, "none": {}, "self": {}, "me": {}, "migrate": {},
	"service": {}, "socket": {}, "target": {}, "timer": {},
	"mount": {}, "path": {}, "slice": {}, "scope": {},
	"device": {}, "swap": {},
}

// ValidateProfileName is the CLI-side copy of the FR-002/FR-003 validator.
func ValidateProfileName(name string) error {
	if !profileNameRegex.MatchString(name) {
		return fmt.Errorf("%w: %q must match %s", ErrInvalidProfileName, name, profileNameRegex)
	}
	if strings.Contains(name, "--") {
		return fmt.Errorf("%w: %q must not contain consecutive hyphens", ErrInvalidProfileName, name)
	}
	if _, ok := reservedProfileNames[strings.ToLower(name)]; ok {
		return fmt.Errorf("%w: %q is reserved", ErrReservedProfileName, name)
	}
	if !filepath.IsLocal(name) {
		return fmt.Errorf("%w: %q is not a local path component", ErrInvalidProfileName, name)
	}
	return nil
}

// ProfileSource identifies where a resolved profile came from.
type ProfileSource int

const (
	SourceFlag       ProfileSource = iota // --profile flag
	SourceEnv                             // WA_PROFILE env var
	SourceActiveFile                      // $XDG_CONFIG_HOME/wa/active-profile
	SourceSingleton                       // exactly one profile on disk
	SourceDefault                         // literal "default"
)

func (s ProfileSource) String() string {
	switch s {
	case SourceFlag:
		return "flag"
	case SourceEnv:
		return "env"
	case SourceActiveFile:
		return "active-profile-file"
	case SourceSingleton:
		return "singleton"
	case SourceDefault:
		return "default"
	default:
		return "unknown"
	}
}

// ResolvedProfile carries the name plus the source that supplied it.
type ResolvedProfile struct {
	Name   string
	Source ProfileSource
}

// ResolveProfile implements the FR-001 precedence chain:
//  1. --profile flag value (non-empty)
//  2. WA_PROFILE env var (non-empty after trim)
//  3. $XDG_CONFIG_HOME/wa/active-profile file (non-empty after trim)
//  4. If exactly one profile exists on disk → that singleton (FR-040)
//  5. Zero profiles → "default" (FR-041)
//  6. Multiple profiles and no hint → ErrMultipleProfiles (FR-039)
//
// Empty/whitespace-only sources fall through to the next level.
func ResolveProfile(flagValue string) (ResolvedProfile, error) {
	// (1) --profile flag.
	if name := strings.TrimSpace(flagValue); name != "" {
		if err := ValidateProfileName(name); err != nil {
			return ResolvedProfile{}, err
		}
		return ResolvedProfile{Name: name, Source: SourceFlag}, nil
	}

	// (2) WA_PROFILE env var.
	if name := strings.TrimSpace(os.Getenv("WA_PROFILE")); name != "" {
		if err := ValidateProfileName(name); err != nil {
			return ResolvedProfile{}, err
		}
		return ResolvedProfile{Name: name, Source: SourceEnv}, nil
	}

	// (3) active-profile file.
	activePath := filepath.Join(xdg.ConfigHome, "wa", "active-profile")
	if data, err := os.ReadFile(activePath); err == nil { //nolint:gosec // path is under validated config home
		// FR-001: trim BOM + whitespace, empty = missing.
		s := strings.TrimPrefix(string(data), "\ufeff")
		s = strings.TrimSpace(s)
		if s != "" {
			if err := ValidateProfileName(s); err != nil {
				return ResolvedProfile{}, fmt.Errorf("active-profile file: %w", err)
			}
			return ResolvedProfile{Name: s, Source: SourceActiveFile}, nil
		}
	}

	// (4/5) enumerate profiles on disk.
	profiles, err := enumerateProfiles()
	if err != nil {
		// Enumeration failure: fall back to default rather than erroring.
		return ResolvedProfile{Name: DefaultProfile, Source: SourceDefault}, nil
	}
	switch len(profiles) {
	case 0:
		return ResolvedProfile{Name: DefaultProfile, Source: SourceDefault}, nil
	case 1:
		return ResolvedProfile{Name: profiles[0], Source: SourceSingleton}, nil
	default:
		// (6) FR-039: multi-profile ambiguity → error with the profile list.
		return ResolvedProfile{}, fmt.Errorf("%w (found: %s)",
			ErrMultipleProfiles, strings.Join(profiles, ", "))
	}
}

// enumerateProfiles globs $XDG_DATA_HOME/wa/*/session.db and returns the
// profile names (directory names) in sorted order. A directory without a
// session.db is NOT listed — it represents an incomplete `wa profile create`.
func enumerateProfiles() ([]string, error) {
	pattern := filepath.Join(xdg.DataHome, "wa", "*", "session.db")
	matches, err := filepath.Glob(pattern)
	if err != nil {
		return nil, err
	}
	names := make([]string, 0, len(matches))
	for _, m := range matches {
		// .../wa/<profile>/session.db → <profile>
		dir := filepath.Dir(m)
		name := filepath.Base(dir)
		if err := ValidateProfileName(name); err != nil {
			// Skip invalid filesystem entries — they render as (invalid)
			// in `wa profile list` instead (FR-025).
			continue
		}
		names = append(names, name)
	}
	return names, nil
}

// listProfilesRaw returns every entry under $XDG_DATA_HOME/wa/ that is a
// directory, regardless of whether it has a session.db or passes the
// validation regex. Used by `wa profile list` to render invalid entries
// with the `(invalid)` marker (FR-025).
func listProfilesRaw() ([]string, error) {
	entries, err := os.ReadDir(filepath.Join(xdg.DataHome, "wa"))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		names = append(names, e.Name())
	}
	return names, nil
}

// socketPathForProfile returns the unix socket path for a profile on this
// platform. Mirrors cmd/wad PathResolver.SocketPath() without depending
// on the daemon package.
func socketPathForProfile(profile string) string {
	return profileSocketPath(profile)
}

// sanitizeProfileName strips control characters and ANSI CSI sequences
// from a profile name sourced from the filesystem, preventing terminal
// escape injection per FR-025 (CVE-2024-52005 precedent).
//
// Returns (sanitized, wasInvalid). If the name contained any non-regex
// bytes, the sanitized form hex-escapes them and wasInvalid is true.
func sanitizeProfileName(raw string) (safe string, invalid bool) {
	if err := ValidateProfileName(raw); err == nil {
		return raw, false
	}
	var b strings.Builder
	for _, c := range []byte(raw) {
		if c < 0x20 || c == 0x7f || c >= 0x80 {
			fmt.Fprintf(&b, "\\x%02x", c)
			continue
		}
		b.WriteByte(c)
	}
	return b.String(), true
}
