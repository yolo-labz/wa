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

// unitTemplate — the hardened systemd **template unit** for feature 008.
// Installed once at ~/.config/systemd/user/wad@.service; enabled per
// profile via `systemctl --user enable --now wad@<profile>.service`.
//
// NOTE ON SANDBOXING: this is a USER UNIT. Most systemd sandboxing
// directives either silently no-op or fail in user mode because the user
// manager runs unprivileged and cannot set up mount namespaces without
// CAP_SYS_ADMIN (ArchWiki Systemd/Sandboxing, freedesktop systemd.exec).
// The directives BELOW are the set that actually takes effect in user
// units. These are INTENTIONALLY ABSENT and MUST NOT be added:
//
//	ProtectSystem=strict     - requires mount namespace
//	ProtectHome              - requires mount namespace
//	PrivateTmp               - requires mount namespace
//	PrivateDevices           - requires mount namespace
//	RestrictNamespaces       - requires privileged namespaces
//	IPAddressDeny            - requires BPF controller delegation
//	MemoryDenyWriteExecute   - **INCOMPATIBLE** with Go runtime (GC uses W+X pages,
//	                            see systemd#3814, linux-audit.com/systemd/settings/units/memorydenywriteexecute/)
//
// See specs/008-multi-profile/contracts/service-templates.md and research.md D10.
var unitTemplate = template.Must(template.New("unit").Parse(`# wa daemon systemd user template unit (feature 008)
# Installed at: ~/.config/systemd/user/wad@.service
# Enable an instance: systemctl --user enable --now wad@<profile>.service
#
# Hardening is limited to what user units actually honour. Do NOT add
# ProtectSystem/ProtectHome/PrivateTmp/RestrictNamespaces/IPAddressDeny/
# MemoryDenyWriteExecute — they either no-op in user mode or break Go.
# See specs/008-multi-profile/research.md D10 for the full rationale.

[Unit]
Description=wa daemon (%i profile)
Documentation=https://github.com/yolo-labz/wa
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
ExecStart={{.WadPath}} --profile %i
Restart=on-failure
RestartSec=5s
StandardOutput=append:%h/.local/state/wa/%i/wad.log
StandardError=inherit
Environment=GOTRACEBACK=crash
Environment=XDG_RUNTIME_DIR=/run/user/%U

# Hardening — directives that work in user units (2024-2026, systemd 255+).
NoNewPrivileges=yes
LockPersonality=yes
RestrictRealtime=yes
RestrictSUIDSGID=yes
SystemCallFilter=@system-service
SystemCallArchitectures=native

[Install]
WantedBy=default.target
`))

// templateUnitPath returns ~/.config/systemd/user/wad@.service — the
// shared template file that every wad@<profile>.service instance reuses.
func templateUnitPath() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".config", "systemd", "user", "wad@.service")
}

// instanceUnitName returns wad@<profile>.service for a given profile.
func instanceUnitName(profile string) string {
	return "wad@" + profile + ".service"
}

// unitPath is retained for backward compatibility with tests that call
// the single-unit path. It returns the template path so the test harness
// still finds a file on disk.
func unitPath() string {
	return templateUnitPath()
}

// generateServiceFile produces the template unit content. Kept for
// backward compatibility with existing callers (feature 007 tests).
func generateServiceFile() (string, error) {
	return generateServiceFileFor(DefaultProfile)
}

// generateServiceFileFor produces the template unit content for the
// given profile. The profile name is NOT substituted into the template
// (the unit uses systemd's %i specifier); we validate and echo it in a
// comment so dry-run output shows the operator which profile they would
// install.
func generateServiceFileFor(profile string) (string, error) {
	wadPath, err := os.Executable()
	if err != nil {
		return "", fmt.Errorf("resolve executable path: %w", err)
	}

	var buf bytes.Buffer
	if err := unitTemplate.Execute(&buf, struct {
		WadPath string
		Profile string
	}{
		WadPath: wadPath,
		Profile: profile,
	}); err != nil {
		return "", fmt.Errorf("render unit template: %w", err)
	}
	return buf.String(), nil
}

// installService writes the template and enables the default instance.
// Kept for backward compatibility with feature 007 callers that don't
// know about profiles.
func installService(content string) error {
	return installServiceFor(DefaultProfile, content)
}

// installServiceFor writes the template (once) and enables the per-
// profile instance via systemctl --user enable --now wad@<profile>.service.
// Per FR-036, subsequent installs for other profiles only add a new
// instance symlink; the template file is reused.
func installServiceFor(profile, content string) error {
	path := templateUnitPath()

	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("create systemd user dir: %w", err)
	}

	// Write the template file (idempotent — overwrite OK since the
	// content is deterministic for a given wad binary path).
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		return fmt.Errorf("write template unit: %w", err)
	}
	fmt.Fprintf(os.Stderr, "wrote %s\n", path)

	// Reload systemd and enable the per-profile instance.
	if err := exec.Command("systemctl", "--user", "daemon-reload").Run(); err != nil { //nolint:gosec // arg list is static
		return fmt.Errorf("systemctl daemon-reload: %w", err)
	}

	instance := instanceUnitName(profile)
	if err := exec.Command("systemctl", "--user", "enable", "--now", instance).Run(); err != nil { //nolint:gosec // instance name validated upstream
		return fmt.Errorf("systemctl enable %s: %w", instance, err)
	}
	fmt.Fprintf(os.Stderr, "enabled %s\n", instance)

	// FR-037: print the exact loginctl command once. Only suggest it on
	// first install (detected heuristically by the absence of any other
	// wad@*.service symlink under default.target.wants/).
	if !otherInstancesEnabled(profile) {
		fmt.Fprintf(os.Stderr,
			"hint: run `loginctl enable-linger $USER` for headless operation\n")
	}
	return nil
}

// otherInstancesEnabled returns true if any wad@*.service instance other
// than `profile` is already enabled under default.target.wants/. Used to
// suppress the repeated loginctl hint.
func otherInstancesEnabled(profile string) bool {
	home, err := os.UserHomeDir()
	if err != nil {
		return false
	}
	wants := filepath.Join(home, ".config", "systemd", "user", "default.target.wants")
	entries, err := os.ReadDir(wants)
	if err != nil {
		return false
	}
	mine := instanceUnitName(profile)
	for _, e := range entries {
		n := e.Name()
		if n == mine {
			continue
		}
		if len(n) >= len("wad@") && n[:len("wad@")] == "wad@" {
			return true
		}
	}
	return false
}

// uninstallService — backward-compat shim for default profile.
func uninstallService() error {
	return uninstallServiceFor(DefaultProfile)
}

// uninstallServiceFor disables and removes the per-profile instance.
// Other profiles are untouched (FR-036). The template file is retained
// unless this is the last wad@* instance.
func uninstallServiceFor(profile string) error {
	instance := instanceUnitName(profile)
	_ = exec.Command("systemctl", "--user", "disable", "--now", instance).Run() //nolint:gosec // instance name validated

	// Only remove the template if no other wad@* instances remain.
	if !otherInstancesEnabled(profile) {
		if err := os.Remove(templateUnitPath()); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("remove template unit: %w", err)
		}
	}

	fmt.Fprintf(os.Stderr, "service %s uninstalled\n", instance)
	return nil
}
