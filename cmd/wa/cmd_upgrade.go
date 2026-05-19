package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
)

var upgradeCmd = &cobra.Command{
	Use:   "upgrade",
	Short: "Print the upgrade command for your install method",
	RunE: func(cmd *cobra.Command, args []string) error {
		hint := upgradeHint()
		fmt.Println(hint)
		return nil
	},
}

// upgradeHint detects the install method from the binary path and returns
// the appropriate upgrade command. Both the raw (pre-symlink-eval) path
// and the real (post-EvalSymlinks) path are needed: NixOS system-profile
// installs land in `/nix/store/...` after symlink resolution but the
// pre-eval path stays at `/run/current-system/sw/bin/wa`, which is the
// only signal that `nix profile upgrade` will not work for this user.
func upgradeHint() string {
	raw, err := os.Executable()
	if err != nil {
		return fallbackHint
	}
	realPath, err := filepath.EvalSymlinks(raw)
	if err != nil {
		return fallbackHint
	}
	return upgradeHintFor(raw, realPath, resolveVersion())
}

const (
	fallbackHint = "https://github.com/yolo-labz/wa/releases/latest"

	// nixSystemProfileHint is the two-line hint for installs surfaced
	// via /run/current-system/, /etc/profiles/per-user/<u>/, or
	// /etc/static/. `nix profile upgrade` fails on these because the
	// upgrade target is a system input, not a user profile entry
	// (spec 110i FR-002).
	nixSystemProfileHint = "ask your NixOS admin to bump the `wa` flake input\n" +
		"user-level workaround: nix profile install github:yolo-labz/wa && export PATH=$HOME/.nix-profile/bin:$PATH"
)

// upgradeHintFor is the pure logic, separated for testability.
//
// rawExe is the path returned by os.Executable() BEFORE symlink resolution
// — needed to distinguish NixOS system-profile installs (which resolve
// into /nix/store via /run/current-system/... or /etc/profiles/...) from
// user nix-profile installs (which resolve into /nix/store directly).
// realExe is the same path AFTER filepath.EvalSymlinks.
func upgradeHintFor(rawExe, realExe, ver string) string {
	switch {
	case strings.Contains(realExe, "/Cellar/") || strings.Contains(realExe, "/homebrew/"):
		return "brew upgrade yolo-labz/tap/wa"
	case strings.HasPrefix(realExe, "/nix/store/") && isNixSystemProfile(rawExe):
		return nixSystemProfileHint
	case strings.HasPrefix(realExe, "/nix/store/"):
		return "nix profile upgrade github:yolo-labz/wa"
	case ver == "dev" || ver == "(devel)":
		return "go install github.com/yolo-labz/wa/v2/cmd/wa@latest && go install github.com/yolo-labz/wa/v2/cmd/wad@latest"
	default:
		return fallbackHint
	}
}

// isNixSystemProfile reports whether rawExe (pre-EvalSymlinks) sits on
// a NixOS system-profile activation path. These paths re-link into
// /nix/store but cannot be upgraded with `nix profile upgrade` — only
// a flake-input bump by the NixOS admin (or `nixos-rebuild switch`)
// promotes a new build.
func isNixSystemProfile(rawExe string) bool {
	return strings.HasPrefix(rawExe, "/run/current-system/") ||
		strings.HasPrefix(rawExe, "/etc/profiles/per-user/") ||
		strings.HasPrefix(rawExe, "/etc/static/")
}
