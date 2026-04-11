# Contract: Service Templates

**Feature**: 008-multi-profile

This contract defines the exact content of the systemd template unit (Linux) and the launchd plist template (macOS) used by `wad install-service --profile <name>`. The templates are rendered by Go's `text/template` package with a well-defined parameter set.

## Template parameters

Both templates receive the same struct at render time:

```go
type ServiceTemplateData struct {
    WadPath     string // absolute path to the wad binary, via os.Executable()
    Profile     string // validated profile name
    XDGStateDir string // expanded per-profile state directory for log paths
    Label       string // reverse-DNS label (darwin only)
    UserName    string // current username (for systemd User= directive if needed)
}
```

## systemd template unit (Linux)

### File location

**Template file** (installed once): `~/.config/systemd/user/wad@.service`

**Instance enablement**: `systemctl --user enable --now wad@<profile>.service` creates a symlink at `~/.config/systemd/user/default.target.wants/wad@<profile>.service` → `../wad@.service`.

### Template content

```ini
# wad@.service — systemd user template unit for wa daemon (one instance per profile)
#
# NOTE ON SANDBOXING:
# This is a USER UNIT. Most systemd sandboxing directives either silently no-op
# or fail in user mode because the user manager runs unprivileged and cannot
# set up mount namespaces without CAP_SYS_ADMIN. Specifically, these directives
# are INTENTIONALLY ABSENT and MUST NOT be added:
#
#   ProtectSystem=strict     - requires mount namespace (no-op in user mode)
#   ProtectHome              - requires mount namespace
#   PrivateTmp               - requires mount namespace
#   PrivateDevices           - requires mount namespace
#   RestrictNamespaces       - requires privileged namespaces
#   IPAddressDeny            - requires BPF controller delegation (not available
#                              for user managers on most distros)
#
# MemoryDenyWriteExecute=yes is INTENTIONALLY ABSENT because Go's garbage
# collector and stack management require writable-executable pages. Enabling
# it causes the daemon to segfault at startup. See systemd issue #3814 and
# https://linux-audit.com/systemd/settings/units/memorydenywriteexecute/.
#
# The directives below are the set that DOES work in user units.

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

# Hardening — directives that work in user units (systemd 255+, 2024-2026)
NoNewPrivileges=yes
LockPersonality=yes
RestrictRealtime=yes
RestrictSUIDSGID=yes
SystemCallFilter=@system-service
SystemCallArchitectures=native

[Install]
WantedBy=default.target
```

### Key decisions

- `%i` is the **instance name specifier** — systemd expands it to the text between `@` and `.service` in the instance name (e.g., `wad@work.service` → `%i = work`).
- `%h` is the home directory specifier — expands to the current user's home.
- **Template is written once**. Multiple `wad install-service --profile` invocations reuse the same template file. Only new symlinks (enablements) are created.
- `ExecStart` uses the **absolute path** to `wad` (resolved via `os.Executable()` at install time and baked into the template). This prevents `$PATH` confusion and matches the existing feature 007 convention.
- `StandardOutput=append:...` writes the daemon's stdout+stderr directly to the per-profile `wad.log` file, mirroring launchd's `StandardOutPath`/`StandardErrorPath` behavior.
- `loginctl enable-linger $USER` is documented as a post-install hint but NOT run automatically (constitution: never run privileged operations implicitly). The hint is printed once on first `install-service` and suppressed on subsequent profile installs.

### Install sequence

```bash
# wad install-service --profile work
mkdir -p ~/.config/systemd/user
cat > ~/.config/systemd/user/wad@.service <<EOF
<template content>
EOF
systemctl --user daemon-reload
systemctl --user enable --now wad@work.service
# Print hint: "Run 'loginctl enable-linger $USER' for headless operation" (once)
```

### Uninstall sequence

```bash
# wad uninstall-service --profile work
systemctl --user disable --now wad@work.service
# Template file is NOT removed — other profiles may share it
# Exception: if wad@*.service returns no results, template is also removed
```

## launchd plist (macOS)

### File location

**Plist file** (one per profile): `~/Library/LaunchAgents/com.yolo-labz.wad.<profile>.plist`

Unlike systemd, launchd has no template mechanism — each profile is a separate plist with a unique `Label`.

### Template content

```xml
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
    <key>Label</key>
    <string>com.yolo-labz.wad.{{.Profile}}</string>

    <key>ProgramArguments</key>
    <array>
        <string>{{.WadPath}}</string>
        <string>--profile</string>
        <string>{{.Profile}}</string>
    </array>

    <!-- KeepAlive as a dict (NOT a bare bool). With SuccessfulExit=false,
         a clean `wa panic` exit does not respawn the daemon into a crash loop.
         With Crashed=true, genuine crashes trigger a restart. -->
    <key>KeepAlive</key>
    <dict>
        <key>Crashed</key>
        <true/>
        <key>SuccessfulExit</key>
        <false/>
    </dict>

    <key>RunAtLoad</key>
    <true/>

    <!-- ProcessType=Background enables throttled CPU/IO scheduling for a
         long-running non-UI daemon. -->
    <key>ProcessType</key>
    <string>Background</string>

    <!-- ThrottleInterval left at the default (10s). Fast-crash loops are
         rate-limited, which is desirable. -->

    <!-- LimitLoadToSessionType is DELIBERATELY ABSENT. Setting it to Aqua
         would block SSH-session invocations of `wa` from reaching the
         daemon. -->

    <key>StandardOutPath</key>
    <string>{{.XDGStateDir}}/wad.log</string>
    <key>StandardErrorPath</key>
    <string>{{.XDGStateDir}}/wad.log</string>

    <!-- launchd gives children an EMPTY PATH by default. Set it explicitly. -->
    <key>EnvironmentVariables</key>
    <dict>
        <key>PATH</key>
        <string>/usr/local/bin:/usr/bin:/bin:/opt/homebrew/bin</string>
        <key>GOTRACEBACK</key>
        <string>crash</string>
    </dict>
</dict>
</plist>
```

### Key decisions

- **Label format**: `com.yolo-labz.wad.<profile>` — reverse-DNS with profile as final component. Label characters are restricted to `[A-Za-z0-9-]` (enforced by the profile name regex).
- **Absolute binary path**: `ProgramArguments[0]` is the absolute path to `wad` as resolved by `os.Executable()` at install time. Prevents `$PATH` confusion.
- **Per-profile log path**: `XDGStateDir` is expanded to `~/Library/Application Support/wa/<profile>/` at install time, so `StandardOutPath` points at that profile's `wad.log`.
- **`KeepAlive` as dict** (not bool): `{Crashed: true, SuccessfulExit: false}` — genuine crashes trigger restart; clean exits do not. Critical for `wa panic` / operator-initiated shutdown.
- **`ProcessType=Background`**: correct for a long-running non-UI daemon; enables throttled CPU/IO scheduling.
- **`EnvironmentVariables.PATH`**: explicit because launchd empties `PATH` for children.
- **`LimitLoadToSessionType` omitted**: intentional — allows SSH-session invocations.
- **`ThrottleInterval` default (10s)**: rate-limits fast-crash loops.
- **Notarisation**: binaries installed via `curl`-fetched GoReleaser tarballs are tagged with `com.apple.quarantine` and will not execute until notarised. Feature 007's `rcodesign` step is on the critical path. Homebrew- and Nix-installed binaries are not affected.
- **RunAtLoad: true** — start on load (required for `launchctl bootstrap` to actually start the job).
- No template mechanism — each `wad install-service --profile X` writes a new plist file.

### Install sequence

```bash
# wad install-service --profile work
mkdir -p ~/Library/LaunchAgents
cat > ~/Library/LaunchAgents/com.yolo-labz.wad.work.plist <<EOF
<rendered plist>
EOF
launchctl bootstrap gui/$(id -u) ~/Library/LaunchAgents/com.yolo-labz.wad.work.plist
```

### Uninstall sequence

```bash
# wad uninstall-service --profile work
launchctl bootout gui/$(id -u)/com.yolo-labz.wad.work
rm ~/Library/LaunchAgents/com.yolo-labz.wad.work.plist
```

### Modern launchctl syntax

The plist uses **launchctl 2.0** syntax (`bootstrap`/`bootout`) rather than the deprecated `load`/`unload`. The 2.0 commands require a **domain specifier** (`gui/$(id -u)` for user agents), which is why the install command embeds `$(id -u)`.

## Validation

Both templates are rendered via `text/template` with:
- `{{.Profile}}` substituted **only after** `ValidateProfileName` has passed — this prevents XML/ini injection since the regex forbids `<>"'&` and whitespace
- `{{.WadPath}}` from `os.Executable()` — typically `/usr/local/bin/wad` or `/opt/homebrew/bin/wad` — trusted source
- `{{.XDGStateDir}}` from `PathResolver.StateDir()` — deterministic, no user input

No user-controlled string is interpolated into either template without validation.

## Enumeration for `wa profile list`

To discover installed service instances:

**Linux**: `systemctl --user list-units 'wad@*.service' --no-legend` → parse unit names → strip `wad@` prefix and `.service` suffix to get profile names.

**darwin**: `launchctl list | grep 'com.yolo-labz.wad\.'` → parse output → strip the `com.yolo-labz.wad.` prefix to get profile names.

**Filesystem fallback** (if launchctl/systemctl unavailable): glob the plist/unit file directory and parse filenames.

## Test coverage requirement

1. **Template rendering test**: both templates render without error for 3 profile names (`default`, `work`, `work-2`). Assert the rendered output contains the expected label/path.
2. **Injection resistance test**: attempt to render with profile names containing XML metacharacters (`<`, `>`, `&`, `"`, `'`) and assert they are rejected by `ValidateProfileName` BEFORE reaching the template.
3. **Golden file test**: pin the exact rendered template for `--profile default` via testscript golden files so format changes are intentional.
4. **Install sequence test**: mocked `systemctl`/`launchctl` commands verified via `exec.Command` captured stdin — assert the install command sequence matches the specification.
