package main

import "testing"

func TestUpgradeHintFor(t *testing.T) {
	tests := []struct {
		name    string
		exePath string
		version string
		want    string
	}{
		{
			name:    "homebrew Cellar path",
			exePath: "/opt/homebrew/Cellar/wa/0.1.0/bin/wa",
			version: "0.1.0",
			want:    "brew upgrade yolo-labz/tap/wa",
		},
		{
			name:    "homebrew linuxbrew path",
			exePath: "/home/linuxbrew/.linuxbrew/homebrew/bin/wa",
			version: "0.1.0",
			want:    "brew upgrade yolo-labz/tap/wa",
		},
		{
			name:    "nix store path",
			exePath: "/nix/store/abc123-wa-0.1.0/bin/wa",
			version: "0.1.0",
			want:    "nix profile upgrade github:yolo-labz/wa",
		},
		{
			name:    "go install devel version",
			exePath: "/home/user/go/bin/wa",
			version: "dev",
			want:    "go install github.com/yolo-labz/wa/cmd/wa@latest && go install github.com/yolo-labz/wa/cmd/wad@latest",
		},
		{
			name:    "go install (devel) version",
			exePath: "/home/user/go/bin/wa",
			version: "(devel)",
			want:    "go install github.com/yolo-labz/wa/cmd/wa@latest && go install github.com/yolo-labz/wa/cmd/wad@latest",
		},
		{
			name:    "fallback unknown path",
			exePath: "/usr/local/bin/wa",
			version: "0.1.0",
			want:    "https://github.com/yolo-labz/wa/releases/latest",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := upgradeHintFor(tt.exePath, tt.version)
			if got != tt.want {
				t.Errorf("upgradeHintFor(%q, %q) = %q, want %q", tt.exePath, tt.version, got, tt.want)
			}
		})
	}
}
