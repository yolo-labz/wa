package main

import (
	"strings"
	"testing"
)

func TestUpgradeHintFor(t *testing.T) {
	tests := []struct {
		name    string
		rawExe  string
		realExe string
		version string
		want    string
	}{
		{
			name:    "homebrew Cellar path",
			rawExe:  "/opt/homebrew/bin/wa",
			realExe: "/opt/homebrew/Cellar/wa/0.1.0/bin/wa",
			version: "0.1.0",
			want:    "brew upgrade yolo-labz/tap/wa",
		},
		{
			name:    "homebrew linuxbrew path",
			rawExe:  "/home/linuxbrew/.linuxbrew/bin/wa",
			realExe: "/home/linuxbrew/.linuxbrew/homebrew/bin/wa",
			version: "0.1.0",
			want:    "brew upgrade yolo-labz/tap/wa",
		},
		{
			name:    "user nix profile",
			rawExe:  "/home/user/.nix-profile/bin/wa",
			realExe: "/nix/store/abc123-wa-0.1.0/bin/wa",
			version: "0.1.0",
			want:    "nix profile upgrade github:yolo-labz/wa",
		},
		{
			name:    "user nix profile (raw == real, no intermediate link)",
			rawExe:  "/nix/store/abc123-wa-0.1.0/bin/wa",
			realExe: "/nix/store/abc123-wa-0.1.0/bin/wa",
			version: "0.1.0",
			want:    "nix profile upgrade github:yolo-labz/wa",
		},
		{
			name:    "go install devel version",
			rawExe:  "/home/user/go/bin/wa",
			realExe: "/home/user/go/bin/wa",
			version: "dev",
			want:    "go install github.com/yolo-labz/wa/v2/cmd/wa@latest && go install github.com/yolo-labz/wa/v2/cmd/wad@latest",
		},
		{
			name:    "go install (devel) version",
			rawExe:  "/home/user/go/bin/wa",
			realExe: "/home/user/go/bin/wa",
			version: "(devel)",
			want:    "go install github.com/yolo-labz/wa/v2/cmd/wa@latest && go install github.com/yolo-labz/wa/v2/cmd/wad@latest",
		},
		{
			name:    "fallback unknown path",
			rawExe:  "/usr/local/bin/wa",
			realExe: "/usr/local/bin/wa",
			version: "0.1.0",
			want:    "https://github.com/yolo-labz/wa/releases/latest",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := upgradeHintFor(tt.rawExe, tt.realExe, tt.version)
			if got != tt.want {
				t.Errorf("upgradeHintFor(%q, %q, %q) = %q, want %q",
					tt.rawExe, tt.realExe, tt.version, got, tt.want)
			}
		})
	}
}

// TestUpgradeHintFor_NixOSSystemProfile asserts spec 110i FR-002:
// NixOS system-profile installs (current-system / per-user profile /
// static activation) MUST surface the two-line hint, not the user-level
// `nix profile upgrade` command which would fail with
// "There are no packages in the profile.".
func TestUpgradeHintFor_NixOSSystemProfile(t *testing.T) {
	tests := []struct {
		name   string
		rawExe string
	}{
		{
			name:   "NixOS current-system activation",
			rawExe: "/run/current-system/sw/bin/wa",
		},
		{
			name:   "NixOS per-user profile",
			rawExe: "/etc/profiles/per-user/notroot/bin/wa",
		},
		{
			name:   "NixOS static activation",
			rawExe: "/etc/static/bin/wa",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := upgradeHintFor(tt.rawExe, "/nix/store/abc123-wa-0.1.0/bin/wa", "0.1.0")
			if !strings.Contains(got, "NixOS admin") {
				t.Errorf("expected NixOS admin hint, got %q", got)
			}
			if !strings.Contains(got, "nix profile install github:yolo-labz/wa") {
				t.Errorf("expected user-level workaround line, got %q", got)
			}
			if strings.HasPrefix(got, "nix profile upgrade") {
				t.Errorf("MUST NOT return user-profile upgrade command for system-profile install, got %q", got)
			}
		})
	}
}
