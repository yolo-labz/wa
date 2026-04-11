//go:build darwin

package main

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"text/template"

	"github.com/adrg/xdg"
)

const plistLabelPrefix = "com.yolo-labz.wad"

// plistTemplate — the hardened launchd plist for feature 008.
//
// Key decisions (see specs/008-multi-profile/contracts/service-templates.md):
//
//   - KeepAlive is a DICT {Crashed: true, SuccessfulExit: false} so a
//     clean `wa panic` exit does not respawn into a crash loop.
//   - ProcessType = Background enables throttled CPU/IO for a long-
//     running non-UI daemon.
//   - EnvironmentVariables.PATH set explicitly — launchd gives children
//     an empty PATH by default.
//   - LimitLoadToSessionType is DELIBERATELY ABSENT so SSH-session
//     invocations also work.
//   - RunAtLoad = true required for launchctl bootstrap to actually
//     start the job.
//   - ThrottleInterval left at default (10s) — rate-limits fast crash
//     loops, which is desirable.
var plistTemplate = template.Must(template.New("plist").Parse(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
    <key>Label</key>
    <string>{{.Label}}</string>

    <key>ProgramArguments</key>
    <array>
        <string>{{.WadPath}}</string>
        <string>--profile</string>
        <string>{{.Profile}}</string>
    </array>

    <!-- KeepAlive as a dict (NOT a bare bool). With SuccessfulExit=false
         a clean wa panic exit does not respawn; Crashed=true still
         restarts on genuine crashes. -->
    <key>KeepAlive</key>
    <dict>
        <key>Crashed</key>
        <true/>
        <key>SuccessfulExit</key>
        <false/>
    </dict>

    <key>RunAtLoad</key>
    <true/>

    <!-- ProcessType=Background enables throttled CPU/IO for a long-
         running non-UI daemon. -->
    <key>ProcessType</key>
    <string>Background</string>

    <!-- LimitLoadToSessionType is DELIBERATELY ABSENT so SSH-session
         invocations also work. -->

    <key>StandardOutPath</key>
    <string>{{.LogPath}}</string>
    <key>StandardErrorPath</key>
    <string>{{.LogPath}}</string>

    <!-- launchd gives children an EMPTY PATH by default — set it. -->
    <key>EnvironmentVariables</key>
    <dict>
        <key>PATH</key>
        <string>/usr/local/bin:/usr/bin:/bin:/opt/homebrew/bin</string>
        <key>GOTRACEBACK</key>
        <string>crash</string>
    </dict>
</dict>
</plist>
`))

// plistLabelFor returns com.yolo-labz.wad.<profile>.
func plistLabelFor(profile string) string {
	return plistLabelPrefix + "." + profile
}

// plistPathFor returns the per-profile plist path.
func plistPathFor(profile string) string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, "Library", "LaunchAgents", plistLabelFor(profile)+".plist")
}

// plistPath — backward-compat shim for the single-profile default.
func plistPath() string {
	return plistPathFor(DefaultProfile)
}

// generateServiceFile — backward-compat shim.
func generateServiceFile() (string, error) {
	return generateServiceFileFor(DefaultProfile)
}

// generateServiceFileFor renders the plist for a given profile.
func generateServiceFileFor(profile string) (string, error) {
	wadPath, err := os.Executable()
	if err != nil {
		return "", fmt.Errorf("resolve executable path: %w", err)
	}
	// Per-profile log path under $XDG_STATE_HOME/wa/<profile>/wad.log.
	logPath := filepath.Join(xdg.StateHome, "wa", profile, "wad.log")

	var buf bytes.Buffer
	if err := plistTemplate.Execute(&buf, struct {
		Label   string
		WadPath string
		Profile string
		LogPath string
	}{
		Label:   plistLabelFor(profile),
		WadPath: wadPath,
		Profile: profile,
		LogPath: logPath,
	}); err != nil {
		return "", fmt.Errorf("render plist template: %w", err)
	}
	return buf.String(), nil
}

// installService — backward-compat shim.
func installService(content string) error {
	return installServiceFor(DefaultProfile, content)
}

// installServiceFor writes the per-profile plist and bootstraps it via
// launchctl 2.0 syntax. Idempotent.
func installServiceFor(profile, content string) error {
	path := plistPathFor(profile)

	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create LaunchAgents dir: %w", err)
	}

	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		return fmt.Errorf("write plist: %w", err)
	}
	fmt.Fprintf(os.Stderr, "wrote %s\n", path)

	// launchctl 2.0: bootstrap gui/<uid> <plist>. The `load` form is
	// deprecated per launchd.plist(5) 2024-2026.
	domain := "gui/" + strconv.Itoa(os.Geteuid())
	if err := exec.Command("launchctl", "bootstrap", domain, path).Run(); err != nil { //nolint:gosec // args are argv
		// Already-loaded is not an error; best-effort bootout first then retry.
		_ = exec.Command("launchctl", "bootout", domain+"/"+plistLabelFor(profile)).Run() //nolint:gosec // args are argv
		if err := exec.Command("launchctl", "bootstrap", domain, path).Run(); err != nil { //nolint:gosec // args are argv
			return fmt.Errorf("launchctl bootstrap: %w", err)
		}
	}
	fmt.Fprintf(os.Stderr, "loaded %s\n", plistLabelFor(profile))
	return nil
}

// uninstallService — backward-compat shim.
func uninstallService() error {
	return uninstallServiceFor(DefaultProfile)
}

// uninstallServiceFor unloads and removes the per-profile plist.
// Idempotent. Other profiles are untouched (FR-036).
func uninstallServiceFor(profile string) error {
	path := plistPathFor(profile)
	domain := "gui/" + strconv.Itoa(os.Geteuid())
	label := plistLabelFor(profile)

	_ = exec.Command("launchctl", "bootout", domain+"/"+label).Run() //nolint:gosec // args are argv

	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("remove plist: %w", err)
	}

	fmt.Fprintf(os.Stderr, "service %s uninstalled\n", label)
	return nil
}
