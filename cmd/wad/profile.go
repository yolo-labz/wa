// Package main is the wad daemon composition root.
//
// This file implements profile name validation and path resolution for
// feature 008 (multi-profile support). See specs/008-multi-profile/spec.md
// FR-001..FR-013 and data-model.md §1-§2 for the contract.
package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/adrg/xdg"

	"github.com/yolo-labz/wa/internal/adapters/primary/socket"
)

// ErrInvalidProfileName is returned when a profile name fails FR-002 regex.
var ErrInvalidProfileName = errors.New("invalid profile name")

// ErrReservedProfileName is returned when a profile name is in the FR-003 list.
var ErrReservedProfileName = errors.New("reserved profile name")

// profileNameRegex encodes FR-002: lowercase 2-32 chars, alpha start,
// alphanumeric end, hyphens allowed (but not as first or last char), no
// other punctuation.
var profileNameRegex = regexp.MustCompile(`^[a-z][a-z0-9-]{0,30}[a-z0-9]$`)

// reservedProfileNames is the case-folded set of names rejected at
// validation time per FR-003. "default" is explicitly NOT reserved — it is
// the canonical default profile name. Comparison is case-insensitive even
// though the regex is lowercase-only, so a future relaxation to mixed case
// does not silently accept a reserved name.
var reservedProfileNames = map[string]struct{}{
	// (a) filesystem specials
	".": {}, "..": {},
	// (b) Windows device names (future Windows port)
	"con": {}, "prn": {}, "aux": {}, "nul": {},
	"com0": {}, "com1": {}, "com2": {}, "com3": {}, "com4": {},
	"com5": {}, "com6": {}, "com7": {}, "com8": {}, "com9": {},
	"lpt0": {}, "lpt1": {}, "lpt2": {}, "lpt3": {}, "lpt4": {},
	"lpt5": {}, "lpt6": {}, "lpt7": {}, "lpt8": {}, "lpt9": {},
	"conin$": {}, "conout$": {},
	// (c) project identifiers
	"wa": {}, "wad": {}, "root": {}, "system": {},
	// (d) systemd/logind user-scope reserved
	"dbus": {}, "systemd": {}, "user": {}, "session": {},
	// (e) subcommand verb collisions with `wa profile <verb>`
	"list": {}, "use": {}, "create": {}, "rm": {}, "show": {},
	"new": {}, "delete": {}, "current": {}, "switch": {},
	"all": {}, "none": {}, "self": {}, "me": {}, "migrate": {},
	// (f) systemd unit-type suffix words (hygiene)
	"service": {}, "socket": {}, "target": {}, "timer": {},
	"mount": {}, "path": {}, "slice": {}, "scope": {},
	"device": {}, "swap": {},
}

// ValidateProfileName enforces FR-002 and FR-003. It is a pure function:
// no filesystem I/O, no allocation beyond the error. Runs in <1ms per SC-005.
func ValidateProfileName(name string) error {
	// FR-002: regex check
	if !profileNameRegex.MatchString(name) {
		return fmt.Errorf("%w: %q must match %s", ErrInvalidProfileName, name, profileNameRegex)
	}
	// FR-002: no consecutive hyphens (git-check-ref-format hygiene)
	if strings.Contains(name, "--") {
		return fmt.Errorf("%w: %q must not contain consecutive hyphens", ErrInvalidProfileName, name)
	}
	// FR-003: reserved list (case-insensitive)
	if _, ok := reservedProfileNames[strings.ToLower(name)]; ok {
		return fmt.Errorf("%w: %q is reserved", ErrReservedProfileName, name)
	}
	// FR-048: defense-in-depth — lexical path-locality check
	if !filepath.IsLocal(name) {
		return fmt.Errorf("%w: %q is not a local path component", ErrInvalidProfileName, name)
	}
	return nil
}

// DefaultProfile is the canonical default profile name. It is NOT in the
// reserved list and passes ValidateProfileName.
const DefaultProfile = "default"

// PathResolver derives all filesystem paths for a given profile. All methods
// return absolute paths that are deterministic for a given profile plus
// environment. See data-model.md §2.
//
// The profile name has already been validated via ValidateProfileName
// before NewPathResolver is called.
type PathResolver struct {
	profile string
}

// NewPathResolver returns a resolver for the given profile, validating the
// name first. Callers that already have a validated name can use the zero
// value via &PathResolver{profile: name}.
func NewPathResolver(profile string) (*PathResolver, error) {
	if err := ValidateProfileName(profile); err != nil {
		return nil, err
	}
	return &PathResolver{profile: profile}, nil
}

// Profile returns the validated profile name.
func (r *PathResolver) Profile() string { return r.profile }

// DataDir returns the per-profile data directory: $XDG_DATA_HOME/wa/<profile>/.
func (r *PathResolver) DataDir() string {
	return filepath.Join(xdg.DataHome, "wa", r.profile)
}

// ConfigDir returns the per-profile config directory: $XDG_CONFIG_HOME/wa/<profile>/.
func (r *PathResolver) ConfigDir() string {
	return filepath.Join(xdg.ConfigHome, "wa", r.profile)
}

// StateDir returns the per-profile state directory: $XDG_STATE_HOME/wa/<profile>/.
func (r *PathResolver) StateDir() string {
	return filepath.Join(xdg.StateHome, "wa", r.profile)
}

// SessionDB returns the session database path (FR-005).
func (r *PathResolver) SessionDB() string {
	return filepath.Join(r.DataDir(), "session.db")
}

// HistoryDB returns the history database path (FR-006).
func (r *PathResolver) HistoryDB() string {
	return filepath.Join(r.DataDir(), "messages.db")
}

// AllowlistTOML returns the per-profile allowlist path (FR-007).
func (r *PathResolver) AllowlistTOML() string {
	return filepath.Join(r.ConfigDir(), "allowlist.toml")
}

// AuditLog returns the per-profile audit log path (FR-008).
func (r *PathResolver) AuditLog() string {
	return filepath.Join(r.StateDir(), "audit.log")
}

// WadLog returns the per-profile daemon log path (FR-009).
func (r *PathResolver) WadLog() string {
	return filepath.Join(r.StateDir(), "wad.log")
}

// SocketPath returns the per-profile unix socket path (FR-010).
// On darwin: ~/Library/Caches/wa/<profile>.sock.
// On Linux:  $XDG_RUNTIME_DIR/wa/<profile>.sock.
// Sockets are flat (not nested) so `ls` enumerates running daemons.
func (r *PathResolver) SocketPath() (string, error) {
	return socket.PathFor(r.profile)
}

// LockPath returns the single-instance lock file path (FR-011):
// `<SocketPath>.lock`, sibling to the socket.
func (r *PathResolver) LockPath() (string, error) {
	s, err := r.SocketPath()
	if err != nil {
		return "", err
	}
	return s + ".lock", nil
}

// PairHTMLPath returns the profile-suffixed pairing HTML path (FR-014):
// os.TempDir()/wa-pair-<profile>.html, to prevent cross-profile collisions.
func (r *PathResolver) PairHTMLPath() string {
	return filepath.Join(os.TempDir(), "wa-pair-"+r.profile+".html")
}

// CacheDir returns the SHARED cache directory (FR-012): $XDG_CACHE_HOME/wa/.
// whatsmeow media is SHA-256 content-addressed so cross-profile sharing is safe.
func (r *PathResolver) CacheDir() string {
	return filepath.Join(xdg.CacheHome, "wa")
}

// ActiveProfileFile returns the top-level active-profile pointer path (FR-013).
func (r *PathResolver) ActiveProfileFile() string {
	return filepath.Join(xdg.ConfigHome, "wa", "active-profile")
}

// SchemaVersionFile returns the top-level schema version file path (FR-013).
func (r *PathResolver) SchemaVersionFile() string {
	return filepath.Join(xdg.ConfigHome, "wa", ".schema-version")
}

// MigratingMarkerFile returns the migration write-ahead marker path.
// See contracts/migration.md §Crash-safety guarantee.
func (r *PathResolver) MigratingMarkerFile() string {
	return filepath.Join(xdg.ConfigHome, "wa", ".migrating")
}

// SocketParentDir returns the shared socket parent directory (FR-042).
// On darwin: ~/Library/Caches/wa/.
// On Linux:  $XDG_RUNTIME_DIR/wa/.
func (r *PathResolver) SocketParentDir() (string, error) {
	s, err := r.SocketPath()
	if err != nil {
		return "", err
	}
	return filepath.Dir(s), nil
}
