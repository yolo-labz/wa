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
// the appropriate upgrade command. Exported for testing via upgradeHintFor.
func upgradeHint() string {
	exe, err := os.Executable()
	if err != nil {
		return fallbackHint
	}
	exe, err = filepath.EvalSymlinks(exe)
	if err != nil {
		return fallbackHint
	}
	return upgradeHintFor(exe, version)
}

const fallbackHint = "https://github.com/yolo-labz/wa/releases/latest"

// upgradeHintFor is the pure logic, separated for testability.
func upgradeHintFor(exePath, ver string) string {
	switch {
	case strings.Contains(exePath, "/Cellar/") || strings.Contains(exePath, "/homebrew/"):
		return "brew upgrade yolo-labz/tap/wa"
	case strings.Contains(exePath, "/nix/store/"):
		return "nix profile upgrade github:yolo-labz/wa"
	case ver == "dev" || ver == "(devel)":
		return "go install github.com/yolo-labz/wa/cmd/wa@latest && go install github.com/yolo-labz/wa/cmd/wad@latest"
	default:
		return fallbackHint
	}
}
