//go:build darwin

package main

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"text/template"

	"github.com/adrg/xdg"
)

const plistLabel = "com.yolo-labz.wad"

var plistTemplate = template.Must(template.New("plist").Parse(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
    <key>Label</key>
    <string>com.yolo-labz.wad</string>
    <key>ProgramArguments</key>
    <array>
        <string>{{.WadPath}}</string>
    </array>
    <key>KeepAlive</key>
    <true/>
    <key>RunAtLoad</key>
    <true/>
    <key>StandardOutPath</key>
    <string>{{.LogPath}}</string>
    <key>StandardErrorPath</key>
    <string>{{.LogPath}}</string>
</dict>
</plist>
`))

func plistPath() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, "Library", "LaunchAgents", plistLabel+".plist")
}

// generateServiceFile produces a launchd plist for wad.
func generateServiceFile() (string, error) {
	wadPath, err := os.Executable()
	if err != nil {
		return "", fmt.Errorf("resolve executable path: %w", err)
	}

	logPath := filepath.Join(xdg.StateHome, "wa", "wad.log")

	var buf bytes.Buffer
	if err := plistTemplate.Execute(&buf, struct {
		WadPath string
		LogPath string
	}{
		WadPath: wadPath,
		LogPath: logPath,
	}); err != nil {
		return "", fmt.Errorf("render plist template: %w", err)
	}

	return buf.String(), nil
}

// installService writes the plist and loads it via launchctl.
func installService(content string) error {
	path := plistPath()

	// Ensure the LaunchAgents directory exists.
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create LaunchAgents dir: %w", err)
	}

	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		return fmt.Errorf("write plist: %w", err)
	}

	fmt.Fprintf(os.Stderr, "wrote %s\n", path)

	if err := exec.Command("launchctl", "load", path).Run(); err != nil {
		return fmt.Errorf("launchctl load: %w", err)
	}

	fmt.Fprintf(os.Stderr, "service loaded via launchctl\n")
	return nil
}

// uninstallService unloads and removes the plist. Idempotent.
func uninstallService() error {
	path := plistPath()

	// Unload — ignore errors (service may not be loaded).
	_ = exec.Command("launchctl", "unload", path).Run()

	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("remove plist: %w", err)
	}

	fmt.Fprintf(os.Stderr, "service uninstalled\n")
	return nil
}
