# Research: Release Packaging and Service Integration

**Feature**: 007-release-packaging
**Date**: 2026-04-10
**Status**: Complete

## D1 — GoReleaser v2 configuration

**Decision**: Use GoReleaser v2 with `version: 2`, two builds (wa, wad), three targets, custom `builds.hooks.post` for rcodesign, and `brews` section for Homebrew tap.

**Rationale**: No `.goreleaser.yaml` exists at the repo root yet. The distribution dossier at `docs/research-dossiers/distribution.md:12-97` has a complete v2 draft. Key v2 changes from v1: `version: 2` mandatory, `archives.formats` (list, not singular), `brews` still works (deprecated for `homebrew_casks` in v2.10+ but functional until v3). rcodesign is NOT a built-in hook — it requires a custom `builds.hooks.post` on darwin builds: `rcodesign sign --pem-source ... {{ .Path }}`. Notarization runs as a post-archive hook via `rcodesign notary-submit --staple`.

**Source**: [GoReleaser v2 docs](https://goreleaser.com/blog/goreleaser-v2/); `docs/research-dossiers/distribution.md`.

---

## D2 — rcodesign notarization from Linux CI

**Decision**: Three-step process: sign → notary-submit → staple (combined with `--staple` flag). Five GitHub Actions secrets needed.

**Process**:
1. `rcodesign sign --code-signature-flags runtime --pem-source <cert> --pem-source <key> <binary>`
2. `rcodesign notary-submit --api-key-path <p8> --wait --staple <binary>`

**Secrets**: `APPLE_DEVELOPER_ID_APPLICATION_CERT` (PEM, base64), `APPLE_DEVELOPER_ID_APPLICATION_KEY` (PEM, base64), `APPLE_API_KEY` (p8, base64), `APPLE_API_KEY_ID`, `APPLE_API_ISSUER`.

**Source**: [rcodesign docs](https://gregoryszorc.com/docs/apple-codesign/stable/apple_codesign_rcodesign_notarizing.html); `docs/research-dossiers/distribution.md:172-213`.

---

## D3 — Nix flake buildGoModule

**Decision**: Use `buildGoModule` (or version-pinned `buildGo126Module`) with `vendorHash`, `subPackages = ["cmd/wa" "cmd/wad"]`, `env.CGO_ENABLED = 0`, and `ldflags`.

**Key fields**:
- `vendorHash` — use `lib.fakeHash` on first build; Nix prints the real hash
- `subPackages` — produces both binaries from one derivation
- `meta.mainProgram = "wa"` — default for `nix run`
- `env.CGO_ENABLED = 0` — constitutional requirement

**Source**: [nixpkgs Go framework](https://github.com/NixOS/nixpkgs/blob/master/doc/languages-frameworks/go.section.md); `docs/research-dossiers/distribution.md:99-147`.

---

## D4 — launchd user agent plist

**Decision**: Standard user agent at `~/Library/LaunchAgents/com.yolo-labz.wad.plist`.

**Key fields**:
```xml
<key>Label</key><string>com.yolo-labz.wad</string>
<key>ProgramArguments</key><array><string>/absolute/path/to/wad</string></array>
<key>KeepAlive</key><true/>
<key>RunAtLoad</key><true/>
<key>StandardOutPath</key><string>$XDG_STATE_HOME/wa/wad.log</string>
<key>StandardErrorPath</key><string>$XDG_STATE_HOME/wa/wad.log</string>
```

Never `LaunchDaemons` (requires root). Load: `launchctl load <path>`. Unload: `launchctl unload <path>`.

**Source**: [launchd.plist(5)](https://keith.github.io/xcode-man-pages/launchd.plist.5.html).

---

## D5 — systemd user unit

**Decision**: User unit at `~/.config/systemd/user/wad.service`.

**Key fields**:
```ini
[Unit]
Description=WhatsApp automation daemon

[Service]
Type=simple
ExecStart=/absolute/path/to/wad
Restart=on-failure
RestartSec=5s

[Install]
WantedBy=default.target
```

Enable: `systemctl --user enable --now wad`. `loginctl enable-linger $USER` is required for the service to survive logout on headless servers — document as a post-install hint.

**Source**: [systemd.service(5)](https://www.freedesktop.org/software/systemd/man/systemd.service.html); [ArchWiki systemd/User](https://wiki.archlinux.org/title/Systemd/User).

---

## Summary — no new Go dependencies

This feature adds only configuration files and ~400 LoC of Go (service generation + upgrade detection). No new Go module dependencies. The GoReleaser, Nix, and CI tooling are external to the Go module.
