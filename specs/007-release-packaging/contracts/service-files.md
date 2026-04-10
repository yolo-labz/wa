# Contract: Service Files

**Feature**: 007-release-packaging

## launchd user agent (macOS)

**Path**: `~/Library/LaunchAgents/com.yolo-labz.wad.plist`

```xml
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
    <key>Label</key>
    <string>com.yolo-labz.wad</string>
    <key>ProgramArguments</key>
    <array>
        <string>{{WAD_PATH}}</string>
    </array>
    <key>KeepAlive</key>
    <true/>
    <key>RunAtLoad</key>
    <true/>
    <key>StandardOutPath</key>
    <string>{{XDG_STATE_HOME}}/wa/wad.log</string>
    <key>StandardErrorPath</key>
    <string>{{XDG_STATE_HOME}}/wa/wad.log</string>
</dict>
</plist>
```

`{{WAD_PATH}}` is the absolute path to the `wad` binary (`os.Executable()`).
`{{XDG_STATE_HOME}}` is resolved at generation time.

## systemd user unit (Linux)

**Path**: `~/.config/systemd/user/wad.service`

```ini
[Unit]
Description=WhatsApp automation daemon (wa)

[Service]
Type=simple
ExecStart={{WAD_PATH}}
Restart=on-failure
RestartSec=5s
Environment=XDG_RUNTIME_DIR=/run/user/%U

[Install]
WantedBy=default.target
```

Post-install hint: `loginctl enable-linger $USER` is required for headless operation.

## Commands

| Action | macOS | Linux |
|---|---|---|
| Install | `wad install-service` | `wad install-service` |
| Dry run | `wad install-service --dry-run` | `wad install-service --dry-run` |
| Uninstall | `wad uninstall-service` | `wad uninstall-service` |
| Load | `launchctl load ~/Library/LaunchAgents/com.yolo-labz.wad.plist` | `systemctl --user enable --now wad` |
| Unload | `launchctl unload ~/Library/LaunchAgents/com.yolo-labz.wad.plist` | `systemctl --user disable --now wad` |
| Logs | `tail -f ~/.local/state/wa/wad.log` | `journalctl --user -u wad -f` |
