# Spec 110i — CLI version + upgrade affordances

Closes #145.

## Problem

NixOS hosts where `wa` is installed via the system module (binary at `/run/current-system/sw/bin/wa` or `/etc/profiles/per-user/<user>/bin/wa`, real path `/nix/store/<hash>-wa-<commit>/bin/wa`) silently pin a stale commit. Users running `wa --remote http://wa.example.com history …` see:

```
$ wa --remote http://wa.example.com history --chat <jid>
unknown flag: --remote
```

There is no surface that says "the flag exists upstream but your build predates it":

1. `wa --version` is rejected as "unknown flag: --version". `wa version` works but is undiscoverable from the error.
2. `wa upgrade` on a `/nix/store/...` path prints `nix profile upgrade github:yolo-labz/wa`. On a NixOS system-profile install this fails with `warning: There are no packages in the profile.` because the upgrade target is a system input, not a user profile entry.
3. The cobra "unknown flag" error has no footer pointing at upgrade or version.

Operator spends ~10 minutes suspecting the daemon's HTTP API before realising the CLI itself is stale (Pedro's UNI-SAVE-STATE.md, 12/05/2026).

## Functional requirements

- **FR-001**: `wa --version` MUST print the same string as `wa version` (default human, `--json` switches to NDJSON schema `wa.version/v1`). Trigger BEFORE cobra subcommand routing so it works on the bare root command, on any subcommand, and on a binary whose subcommands have drifted (forward compat).
- **FR-002**: `upgradeHintFor` MUST distinguish NixOS system-profile installs from user nix-profile installs. System-profile detection: real path starts with `/nix/store/` AND `os.Executable()` (pre-EvalSymlinks) starts with `/run/current-system/`, `/etc/profiles/per-user/`, or `/etc/static/`. For system-profile installs, return a two-line hint:
  ```
  ask your NixOS admin to bump the `wa` flake input
  user-level workaround: nix profile install github:yolo-labz/wa && export PATH=$HOME/.nix-profile/bin:$PATH
  ```
- **FR-003**: When `rootCmd.Execute()` returns a cobra `unknown flag` or `unknown command` error, `main.run` MUST append a footer to stderr:
  ```
  Build <version>. If the flag/command is documented but unrecognized, your build may be stale.
  Upgrade: <upgradeHint()>
  ```
  This footer MUST be omitted for unrelated errors (network failures, JSON-RPC errors) so log noise stays low.
- **FR-004**: All three behaviours surface in `wa version --json` schema as `{"schema":"wa.version/v1","version":"<v>"}`. No schema bump required — same `v1` shape.

## Non-functional

- No new Go dependencies.
- Zero changes to JSON-RPC wire protocol.
- Build-tag agnostic (Linux + Darwin).
- CGO_ENABLED=0 invariant holds (no nix interop).

## Rejected alternatives

1. **In-process self-update.** `wa upgrade` actually downloads a new binary. Rejected per CLAUDE.md anti-pattern §9 — no signing chain, breaks SELinux/macOS Gatekeeper expectations.
2. **Embed the full feature catalogue at build time and compare against requested flag.** Too brittle; cobra already emits the right error verbatim. We just need a footer.
3. **Force users onto `nix profile add` always.** Rejected — system-profile install is a legitimate sysadmin choice; the user-level override is a workaround, not the canonical path.
4. **Read NixOS detection via `/etc/NIXOS` marker file.** Rejected — the marker exists on every NixOS box including ones that installed `wa` via `go install` or `brew`. Real-path prefix is the discriminator.

## Test plan

- `cmd_upgrade_test.go`: extend table with NixOS system-profile cases (`/run/current-system/sw/bin/wa`, `/etc/profiles/per-user/notroot/bin/wa`, `/etc/static/bin/wa`). Assert two-line output via `strings.Contains`.
- `cmd_version_test.go` (new): `wa --version` flag prints `wa version <v>`, `wa --version --json` prints schema, exits 0. Use `rootCmd.SetArgs` + `bytes.Buffer` stdout capture.
- `main_unknown_flag_test.go` (new): inject an `unknown flag` error into `appendStaleHint` (pure function) and assert the footer string. Cobra's error matching: `strings.Contains(err.Error(), "unknown flag") || strings.Contains(err.Error(), "unknown command")`.

## Implementation

Files touched:
- `cmd/wa/cmd_version.go` — add `versionFlag` bool persistent flag; PreRun shim that intercepts before subcommand dispatch.
- `cmd/wa/cmd_upgrade.go` — extend `upgradeHintFor` with NixOS system-profile detection; expose pre-symlink-eval path so detection sees `/run/current-system/...` before `filepath.EvalSymlinks`.
- `cmd/wa/main.go` — append stale-build hint to stderr on cobra unknown-flag/command errors.

LoC budget: ≤120 lines of source + ≤80 lines of test.
