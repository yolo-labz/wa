//go:build linux

package main

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"text/template"
)

var unitTemplate = template.Must(template.New("unit").Parse(`[Unit]
Description=WhatsApp automation daemon (wa)

[Service]
Type=simple
ExecStart={{.WadPath}}
Restart=on-failure
RestartSec=5s
Environment=XDG_RUNTIME_DIR=/run/user/%U

[Install]
WantedBy=default.target
`))

func unitPath() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".config", "systemd", "user", "wad.service")
}

// generateServiceFile produces a systemd user unit for wad.
func generateServiceFile() (string, error) {
	wadPath, err := os.Executable()
	if err != nil {
		return "", fmt.Errorf("resolve executable path: %w", err)
	}

	var buf bytes.Buffer
	if err := unitTemplate.Execute(&buf, struct {
		WadPath string
	}{
		WadPath: wadPath,
	}); err != nil {
		return "", fmt.Errorf("render unit template: %w", err)
	}

	return buf.String(), nil
}

// installService writes the unit file and enables it via systemctl.
func installService(content string) error {
	path := unitPath()

	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("create systemd user dir: %w", err)
	}

	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		return fmt.Errorf("write unit file: %w", err)
	}

	fmt.Fprintf(os.Stderr, "wrote %s\n", path)

	if err := exec.Command("systemctl", "--user", "enable", "--now", "wad").Run(); err != nil {
		return fmt.Errorf("systemctl enable: %w", err)
	}

	fmt.Fprintf(os.Stderr, "service enabled via systemctl\n")
	fmt.Fprintf(os.Stderr, "hint: run `loginctl enable-linger $USER` for headless operation\n")
	return nil
}

// uninstallService disables and removes the unit file. Idempotent.
func uninstallService() error {
	path := unitPath()

	// Disable — ignore errors (service may not be enabled).
	_ = exec.Command("systemctl", "--user", "disable", "--now", "wad").Run()

	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("remove unit file: %w", err)
	}

	fmt.Fprintf(os.Stderr, "service uninstalled\n")
	return nil
}
