# Research: Multi-Profile Support

**Feature**: 008-multi-profile
**Date**: 2026-04-11
**Source**: 5 parallel research agents with web access

This document captures the architectural decisions for feature 008, each backed by prior art and primary-source citations per constitution principle V. The decisions inform the spec's functional requirements and the subsequent implementation.

---

## D1 — Dominant multi-profile pattern (swarm 2.1)

**Decision**: Hybrid — adopt gpg-agent's **per-profile directory** model for state isolation and aws/kubectl's **runtime selection UX** (`--profile` flag + `WA_PROFILE` env var + default named `default`). Reject the multi-tenant single-daemon model (one `wad` process holding N whatsmeow clients).

**Rationale**: Eight comparable tools surveyed (gpg-agent, 1Password, kubectl, aws-cli, tailscaled, rclone, docker, ssh). Five use "single config file with named sections + runtime selector + current-context pointer" (aws/kubectl/docker/1Password/tailscale). But **gpg-agent is the closest architectural match for `wa`** because it's the only one where each profile needs its own long-running daemon, own socket, own encrypted ratchet store, and own audit log. gpg solves this by making the **homedir** the unit of isolation. wa's `session.db` is a SQLite file with whatsmeow's Signal-protocol ratchets — it physically cannot hold two sessions, which forces a per-directory layout.

The runtime UX (`--profile` flag) comes from the aws/kubectl convention because users expect it from every modern CLI. Default profile name `default` matches AWS literal `[default]` section and Docker's `default` context — the least-surprising choice per the aws-cli lesson that empty-profile semantics confuse users.

**Rejected alternative — multi-tenant daemon**: `signal-cli` DOES ship a JSON-RPC daemon mode with a multi-account variant (`signal-cli --account=...`) that proves the design is workable at this scale ([signal-cli discussion #799](https://github.com/AsamK/signal-cli/discussions/799)). We reject it nonetheless because **Signal/WhatsApp ratchet corruption is silent and catastrophic**: a bug that leaks state across profiles inside one process can desync one or both accounts permanently, and the desync is not detected until the next message fails to decrypt on the peer side. Process-level isolation via one `wad` per profile bounds the blast radius of such a bug to a single account. Signal-cli mitigates the same risk via extensive integration testing that `wa` does not have the test infrastructure to match in v0. The cost is ~30 MB RSS per profile, documented as an explicit trade-off in the Assumptions section of spec.md. When/if `wa` gains the same integration-test coverage, this decision can be revisited.

**Rejected alternative — AWS-cli style single-config-file with `[profile work]` sections**: rejected because the session database physically cannot be a section in a TOML file, and mixing "sectioned config" with "per-profile binary state dir" creates two sources of truth. Filesystem-glob enumeration (D5) is simpler.

**Rejected alternative — gh-CLI "switch then run" with no `--profile` flag**: rejected because explicit `--profile` is better for scripting, for `ps aux` visibility (D4), and for unambiguous systemd/launchd service arguments.

**Sources**:
- [gpg --homedir docs](https://www.gnupg.org/documentation/manuals/gnupg/GPG-Configuration.html)
- [1Password CLI account docs](https://developer.1password.com/docs/cli/reference/commands/account/)
- [kubectl multi-cluster config](https://kubernetes.io/docs/concepts/configuration/organize-cluster-access-kubeconfig/)
- [AWS CLI configuration files](https://docs.aws.amazon.com/cli/latest/userguide/cli-configure-files.html)
- [Docker contexts](https://docs.docker.com/engine/manage-resources/contexts/)
- [rclone config reference](https://rclone.org/docs/#config-file)
- [signal-cli JSON-RPC daemon discussion #799](https://github.com/AsamK/signal-cli/discussions/799) — the strongest rejected alternative
- [aws-cli empty-profile regression #3431](https://github.com/aws/aws-cli/issues/3431) — motivation for FR-001 empty-string handling
- [gh CLI multiple-accounts](https://github.com/cli/cli/blob/trunk/docs/multiple-accounts.md)
- `man ssh_config` for Host block pattern

---

## D2 — systemd template units (swarm 2.2)

**Decision**: Use a **systemd template unit** `wad@.service` installed once, enabled per-profile via `systemctl --user enable wad@work.service`.

**Key mechanics**:
- Template file: `~/.config/systemd/user/wad@.service` (one file)
- Enable: `systemctl --user enable wad@work.service` creates a symlink under `default.target.wants/`
- Inside the template: `ExecStart=%h/.local/bin/wad --profile %i` where `%i` is the instance name
- `loginctl enable-linger $USER` is **per-user, not per-instance** — call once at first `install-service`, skip afterwards
- `systemctl --user daemon-reload` re-reads unit files but does NOT restart running instances; explicit `restart wad@<profile>.service` needed
- `systemctl --user list-units 'wad@*'` enumerates active instances — no sidecar registry needed

**Sources**:
- [systemd.unit(5) — specifier table](https://www.freedesktop.org/software/systemd/man/latest/systemd.unit.html)
- [Fedora Magazine: systemd template unit files](https://fedoramagazine.org/systemd-template-unit-files/)
- [ArchWiki: systemd/User](https://wiki.archlinux.org/title/Systemd/User)

---

## D3 — launchd multi-instance (swarm 2.2)

**Decision**: **One plist file per profile** — launchd has no template mechanism equivalent to systemd's `%i`. The `Label` field must be unique per instance.

**Conventions**:
- Label: `com.yolo-labz.wad.<profile>` (reverse-DNS with profile as final component)
- Plist path: `~/Library/LaunchAgents/com.yolo-labz.wad.<profile>.plist`
- `ProgramArguments` = `["/opt/homebrew/bin/wad", "--profile", "<profile>"]`
- Install: `launchctl bootstrap gui/$(id -u) <plist>` (modern 2.0 syntax; `launchctl load` is deprecated)
- Uninstall: `launchctl bootout gui/$(id -u)/com.yolo-labz.wad.<profile>` + `rm` the plist
- Enumerate installed profiles: glob `~/Library/LaunchAgents/com.yolo-labz.wad.*.plist`

Label character set follows DNS-label rules (RFC 1035/1123): `[A-Za-z0-9-]` only. No underscores, no spaces, no dots inside the profile segment (dots are RDN separators).

**Sources**:
- [launchd.plist(5)](https://keith.github.io/xcode-man-pages/launchd.plist.5.html)
- [launchd.info tutorial](https://www.launchd.info/)
- [Apple: Creating Launchd Jobs](https://developer.apple.com/library/archive/documentation/MacOSX/Conceptual/BPSystemStartup/Chapters/CreatingLaunchdJobs.html)

---

## D4 — CLI flag over env var (swarm 2.2)

**Decision**: Profile is primarily selected via `--profile <name>` CLI flag, with `WA_PROFILE` env var as fallback. `wad` receives `--profile` as a CLI argument in both launchd `ProgramArguments` and systemd `ExecStart`. Env vars are NOT the primary discriminator.

**Rationale**:
1. Visible in `ps aux` and Activity Monitor — operators can see which profile is which without `cat /proc/$PID/environ`
2. Survives `os.Args` introspection for `wad status`
3. Avoids env var leakage into child processes
4. Matches prior art — signal-cli uses `--config` flag, tailscaled uses `--state`/`--socket` flags, both distinguish instances via args

**Sources**: signal-cli + tailscale docs; general CLI conventions.

---

## D5 — XDG path layout (swarm 2.3)

**Decision**: **Option A with a flat socket directory** — per-profile subdirectory under each XDG base for state, flat filenames for runtime sockets.

**Concrete layout**:
```
$XDG_DATA_HOME/wa/<profile>/session.db
$XDG_DATA_HOME/wa/<profile>/messages.db
$XDG_CONFIG_HOME/wa/<profile>/allowlist.toml
$XDG_CONFIG_HOME/wa/config.toml              # top-level index, [profile.*] sections
$XDG_CONFIG_HOME/wa/active-profile           # one-line file, name only
$XDG_STATE_HOME/wa/<profile>/audit.log
$XDG_STATE_HOME/wa/<profile>/wad.log
$XDG_RUNTIME_DIR/wa/<profile>.sock            # FLAT — discovery surface
$XDG_RUNTIME_DIR/wa/<profile>.lock            # sibling flock
$XDG_CACHE_HOME/wa/thumbnails/                # SHARED across profiles, content-addressed
```

**Rationale**:
1. **signal-cli + step (smallstep) precedent**: both put per-profile state in subdirs under the XDG app root. Matches `$XDG_DATA_HOME/signal-cli/data/<phonenumber>/` exactly.
2. **Shell globbing friendliness**: `ls ~/.local/share/wa/` lists profiles directly; `rm -rf ~/.local/share/wa/work` nukes one profile.
3. **Shared cache possible**: media thumbnails and download cache stay flat at `$XDG_CACHE_HOME/wa/` because whatsmeow media is SHA-256-addressed and cross-profile sharing is safe.
4. **Socket is flat**: because the socket IS the daemon-discovery surface (`wa --profile work` must find `work.sock` without reading an index), keep sockets flat in a single directory. Matches Emacs's `$XDG_RUNTIME_DIR/emacs/server` and systemd's `foo@instance.socket` ergonomics.
5. **XDG spec compliance**: spec is silent on profile namespacing, treats everything below `<app>/` as application's private business. All three layouts (subdir / prefix / filename-suffix) are spec-compliant; pick based on ergonomics.

**Rejected alternatives**:
- **Option B** (`$XDG_DATA_HOME/wa-<profile>/`): pollutes XDG parent with siblings, prevents shared cache, violates the spec's "one subdir per app" guidance.
- **Option C** (`session-<profile>.db` in flat `wa/`): breaks down for allowlist hot-reload watcher, audit log rotation, and socket discovery.

**Sources**:
- [XDG Base Directory Specification](https://specifications.freedesktop.org/basedir-spec/basedir-spec-latest.html)
- [adrg/xdg](https://github.com/adrg/xdg) — confirms library has zero profile awareness; callers `filepath.Join` their own hierarchies
- [signal-cli manpage](https://github.com/AsamK/signal-cli/blob/master/man/signal-cli.1.adoc) — precedent for per-account subdirs
- [smallstep step CLI contexts](https://smallstep.com/docs/step-cli/the-step-command/) — precedent for authorities/profiles split
- [Emacs server-socket-dir discussion](https://lists.gnu.org/r/emacs-devel/2019-02/msg00045.html)
- [cli/cli #554 XDG compliance](https://github.com/cli/cli/issues/554)

---

## D6 — Profile name validation (swarm 2.4)

**Decision**: Regex `^[a-z][a-z0-9-]{0,30}[a-z0-9]$` with max length 32. Reserved names rejected.

**Full rules**:
- Length: 2-32 characters
- Character set: lowercase letters, digits, hyphens only
- Must start with a letter (RFC 1123 DNS label rule; avoids numeric-only collision with IDs)
- Must end with alphanumeric (forbids trailing hyphen)
- Reserved names rejected: `default` (reserved for the implicit profile semantically, though the string `default` IS the default profile — edit: `default` is ALLOWED because it's THE default), `.`, `..`, `con`, `prn`, `aux`, `nul`, `com1..com9`, `lpt1..lpt9`, `root`, `system`, `wa`, `wad`
- **Correction**: `default` is NOT reserved — it's the canonical default profile name, so it must be valid.

**Per-constraint justification**:
- **Lowercase only**: avoids case-folding mismatches on HFS+/APFS (case-insensitive) vs ext4 (case-sensitive). Matches kubectl/Docker/Homebrew.
- **Hyphen-only separator** (no underscore): launchd RDN convention discourages underscores; purely cosmetic.
- **No dots**: systemd reserves dots, confuses `.sock`/`.plist` suffixes, enables `..` path traversal.
- **No `@`**: systemd template separator (`wad@<profile>@foo.service` is legal but confusing).
- **No slashes, whitespace, XML metachars (`<>"'&`), null byte**: filesystem + XML + shell injection prevention.
- **Max 32**: driven by darwin `sun_path` 104-byte limit. Budget: `/Users/<user≤32>/Library/Caches/wa/<profile>.sock\0` = 33 + 32 + len(profile) + 5 = 70 + len(profile). Leaves 34 bytes headroom; cap profile at 32 for safety.

**Runtime guard**: `wad` startup MUST compute `len(socketPath) < 104` on darwin and refuse to start with a clear error if exceeded (for users with unusually long home directories).

**Sources**:
- [systemd.unit(5)](https://www.freedesktop.org/software/systemd/man/latest/systemd.unit.html)
- [launchd.plist(5) Label key](https://keith.github.io/xcode-man-pages/launchd.plist.5.html)
- [Kubernetes object naming — RFC 1123 subdomain](https://kubernetes.io/docs/concepts/overview/working-with-objects/names/)
- [git-check-ref-format](https://git-scm.com/docs/git-check-ref-format)
- [Microsoft: Naming Files, Paths, Namespaces](https://learn.microsoft.com/en-us/windows/win32/fileio/naming-a-file)
- [Unix socket path length — 8-p.info](https://blog.8-p.info/en/2020/06/11/unix-domain-socket-length/)

---

## D7 — Migration strategy (swarm 2.5, revised post-swarm-3)

**Decision**: **Automatic lazy migration** on first run of the 008 binary, using a **write-ahead marker + staging directory + single pivot** pattern for crash safety. Hybrid of git's `index.lock` discipline, etcd's write-ahead migration log, and the SQLite WAL-checkpoint idiom. Explicit `wa migrate` subcommand for forensic runs.

**CRITICAL correction from swarm 2.5**: the original swarm 2.5 proposal described the migration as "atomic via flock + POSIX rename(2)". This was a category error. POSIX guarantees atomicity of a **single** `rename(2)` call, not of a sequence of N renames. A crash between rename #2 and rename #3 leaves the filesystem in a half-migrated state that is indistinguishable from pre-migration or mid-migration. The corrected design (below) uses a staging directory and a single pivot rename, with a persistent marker file that allows startup recovery to complete or roll back an interrupted migration.

**CRITICAL correction #2**: the original migration set (`session.db`, `messages.db`, `allowlist.toml`, `audit.log`, `wad.log`) omitted SQLite's **WAL/SHM sidecar files**. `modernc.org/sqlite` with `journal_mode=WAL` (the default for `sqlitestore` and `sqlitehistory`) maintains `session.db-wal` and `session.db-shm` alongside the main file. Moving only `session.db` while leaving the WAL behind results in **silent data loss of any committed-but-not-checkpointed transactions** ([SQLite WAL docs §Backwards Compatibility](https://www.sqlite.org/wal.html)). The corrected sequence issues `PRAGMA wal_checkpoint(TRUNCATE)` on every `*.db` file before staging.

**Migration sequence** (corrected):
1. On `wad` startup with schema-version < 2 (or absent) and legacy layout detected:
2. Assert no other process holds `session.db` open; acquire flock on `session.db.lock`.
3. Pre-flight: compare `statx`/`Stat_t.Dev` of source and destination parents; if they differ (cross-filesystem), abort with `EXDEV` typed error before any mutation.
4. For every `*.db` in the move set, open the database, issue `PRAGMA wal_checkpoint(TRUNCATE)`, close — this collapses the WAL into the main file so the `-wal` and `-shm` sidecars become empty or disappear.
5. Write `$XDG_CONFIG_HOME/wa/.migrating` marker file containing the planned move list + timestamp; `fsync` the marker and its parent directory.
6. Create `$XDG_DATA_HOME/wa/default.new/` staging directory. **Copy** (not move) every source file into its final position under `default.new/`, `fsync` each destination file, then `fsync` `default.new/` and its parent.
7. Perform the single pivot:
   - **Linux ≥3.15**: `renameat2(wa, "default.new", wa, "default", RENAME_EXCHANGE)` — atomic swap of the staging directory with a (possibly empty or pre-existing) `default` entry.
   - **darwin / older Linux**: two-step fallback `rename("default", "default.old"); rename("default.new", "default")`, recorded in the `.migrating` marker so a crash between the two renames is recoverable on the next startup.
8. `fsync` the pivot parent directory. On darwin, use `fcntl(F_FULLFSYNC)` — plain `fsync` is documented as insufficient for power-loss durability on APFS ([Apple `fsync(2)` man page](https://developer.apple.com/library/archive/documentation/System/Conceptual/ManPages_iPhoneOS/man2/fsync.2.html)).
9. Atomically write `$XDG_CONFIG_HOME/wa/.schema-version` = `2\n` (tempfile → fsync → rename → fsync parent).
10. Atomically write `$XDG_CONFIG_HOME/wa/active-profile` = `default\n` (same idiom).
11. Append one audit event to `$XDG_STATE_HOME/wa/default/audit.log`: action `migrate`, actor `wad:migrate`, decision `ok`, detail `legacy single-profile → default/ (schema v1 → v2)`.
12. Unlink `default.old/` (if the two-step fallback was used). Delete the `.migrating` marker.
13. Release flock; continue startup.

**Startup recovery rule**: if `.migrating` exists on startup, the daemon MUST read the marker and either (a) complete the pivot if step 7 was partially done (by finishing the two-step rename), or (b) roll back by removing `default.new/` and/or moving `default.old/` back to `default/`, then unlink the marker. This is the same recovery discipline as git's `index.lock` and etcd's write-ahead log.

**Why `fsync` on parent directories matters**: on ext4 with `data=ordered` (default), a `rename` is a metadata operation committed via the journal; without `fsync(parent_dir_fd)` a post-crash reboot can see the rename as never-happened even though the renamed file's contents are intact. See Jeff Moyer, LWN 457667 "Ensuring data reaches disk" and `Documentation/filesystems/ext4/journal.rst`.

**Why a write-ahead marker matters**: the marker is the **single source of truth** for "is a migration in progress". Without it, startup cannot distinguish "half-done migration" from "user manually moved files around". The marker converts the recovery problem from observation-based to log-based — the same shift etcd v3 made over etcd v2.

**Idempotency**: schema-version-branched. `v2 → noop`, `v1 → migrate`, absent → treat as `v1`. Presence of `.migrating` triggers recovery first, then the branch.

**Crash-injection test requirement**: SC-013 mandates a fault-injection test that `SIGKILL`s the migration process between every pair of numbered steps and asserts recovery correctness. `testing/synctest` is explicitly the wrong tool (virtualises time, not filesystem); use `t.TempDir()` + `exec.Command` subprocess kills instead.

**Active-profile resolution order** (same as kubectl + Docker):
1. `--profile` flag on CLI
2. `WA_PROFILE` environment variable
3. `$XDG_CONFIG_HOME/wa/active-profile` file contents
4. Literal string `"default"`

**Multi-profile ambiguity handling**:
- If `--profile` omitted AND multiple profiles exist AND no active-profile file: **error exit 78** with message `multiple profiles exist (default, work); pass --profile or run 'wa profile use <name>'`
- If exactly one profile exists: silently use it (matches Docker's default)
- If zero profiles exist (fresh install): use `default` and create it on pair

**New subcommands in feature 008**:
- `wa profile list` — lists `dataHome/wa/*/session.db` globs, stars the active one
- `wa profile use <name>` — writes `active-profile` file
- `wa profile create <name>` — mkdirs the tree, triggers `wa pair --profile <name>`
- `wa profile rm <name>` — refuses if `<name>` is active OR if it's the only profile OR if `--force` not passed
- `wa migrate [--dry-run|--rollback]` — explicit form of the auto-migration

**Cobra shell completion**: `RegisterFlagCompletionFunc("profile", completeProfileNames)` where `completeProfileNames` reads `dataHome/wa/*/session.db` via `filepath.Glob` and returns `(names, cobra.ShellCompDirectiveNoFileComp)`. Works across bash/zsh/fish/powershell.

**Minimum-surprise contract**: A user who has never heard of profiles types `wa status`, it migrates silently on first 008 run, and they never see the word "profile" unless they opt in. This matches the AWS CLI contract.

**Release-notes requirement**: the 008 release notes MUST say: *"wa transparently migrates your single-profile installation to a `default` profile on first run. No action is required. If anything goes wrong, `wa migrate --rollback` restores the prior layout."*

**Sources**:
- [gh CLI multiple-accounts docs](https://github.com/cli/cli/blob/trunk/docs/multiple-accounts.md)
- [gh CLI 2.40.0 discussion](https://github.com/cli/cli/discussions/8429)
- [direnv #406 + #574](https://github.com/direnv/direnv/issues/406)
- [Docker contexts](https://docs.docker.com/engine/manage-resources/contexts/)
- [kubectl current-context](https://kubernetes.io/docs/reference/kubectl/generated/kubectl_config/kubectl_config_current-context/)
- [AWS CLI config files](https://docs.aws.amazon.com/cli/v1/userguide/cli-configure-files.html)
- [Cobra completions](https://github.com/spf13/cobra/blob/main/site/content/completions/_index.md)
- [SQLite WAL documentation](https://www.sqlite.org/wal.html) — §Backwards Compatibility, §Avoiding Excessively Large WAL Files
- [SQLite atomic commit](https://www.sqlite.org/atomiccommit.html) — crash-recovery model
- [POSIX rename(2)](https://man7.org/linux/man-pages/man2/rename.2.html) — single-call atomicity, EXDEV
- [Linux renameat2(2)](https://man7.org/linux/man-pages/man2/renameat2.2.html) — RENAME_EXCHANGE semantics
- [Linux kernel ext4 journal docs](https://www.kernel.org/doc/html/latest/filesystems/ext4/journal.html)
- [LWN 457667 — Ensuring data reaches disk (Jeff Moyer)](https://lwn.net/Articles/457667/)
- [Apple fsync(2) man page](https://developer.apple.com/library/archive/documentation/System/Conceptual/ManPages_iPhoneOS/man2/fsync.2.html) — F_FULLFSYNC requirement on APFS
- [etcd server/storage/backend — migration WAL pattern](https://github.com/etcd-io/etcd/tree/main/server/storage/backend)
- [git lockfile.c — .lock + rename pivot pattern](https://github.com/git/git/blob/master/lockfile.c)

---

## D9 — Unix domain socket security posture (swarm 3.2, added 2026-04-11)

**Decision**: Adopt a defense-in-depth stack on top of the existing `0600` permissions and same-UID design: (a) verify socket parent directory state before bind; (b) close the bind-to-chmod TOCTOU with a narrowed umask; (c) `O_NOFOLLOW` on lockfile open; (d) explicit peer-credential check on every accept.

**Findings**:

1. **`SO_PEERCRED` (Linux) / `LOCAL_PEEREPID` + `getpeereid(2)` (darwin) remain correct**. Credentials are captured at `connect()` time and are immutable afterwards. See [golang/go#41659](https://github.com/golang/go/issues/41659), [toolman.org/net/peercred](https://pkg.go.dev/toolman.org/net/peercred).

2. **`SO_PASSCRED`/`SCM_CREDENTIALS` is NOT needed**. That's for per-message credentials on `SOCK_DGRAM`; `wa` uses `SOCK_STREAM`.

3. **CVE-2025-68146 (filelock TOCTOU symlink, Nov 2025)**: the fix was `O_NOFOLLOW` on the lockfile open. Go's `rogpeppe/go-internal/lockedfile` opens with `O_CREATE|O_RDWR` **without** `O_NOFOLLOW`. We MUST wrap or vendor a patched variant. See [tox-dev/filelock PR #461](https://github.com/tox-dev/filelock/pull/461).

4. **TOCTOU on bind**: `net.Listen("unix", path)` calls `bind(2)` which does NOT set mode atomically — the socket is briefly visible with umask-derived perms before any `os.Chmod`. Mitigation: narrow `syscall.Umask(0177)` around the listen call, or bind inside a verified-`0700` parent. The parent-dir approach is preferred.

5. **macOS sandbox**: a client CLI invocation from inside an App Sandbox container CANNOT connect to the unix socket regardless of permissions. Documented non-goal. See [Apple DTS forum 126059](https://developer.apple.com/forums/thread/126059).

6. **darwin `sun_path` limit is still 104 bytes**, unchanged in xnu since 4.4BSD. The check must count the NUL terminator: effective limit is 103 printable bytes.

7. **Stale-socket cleanup must be lock-guarded**: unconditional unlink+bind at startup races if two daemons attempt startup simultaneously. Correct sequence: acquire `.lock` first, then unlink stale socket, then bind new socket.

**Sources**:
- [CVE-2025-68146 filelock TOCTOU](https://advisories.gitlab.com/pkg/pypi/filelock/CVE-2025-68146/)
- [Go lockedfile source](https://cs.opensource.google/go/go/+/refs/tags/go1.25.0:src/cmd/go/internal/lockedfile/)
- [Apple getpeereid(3) man page](https://developer.apple.com/library/archive/documentation/System/Conceptual/ManPages_iPhoneOS/man3/getpeereid.3.html)
- [unix(7) Linux man page](https://man7.org/linux/man-pages/man7/unix.7.html)
- [8-p.info UDS path length limits](https://blog.8-p.info/en/2020/06/11/unix-domain-socket-length/)

---

## D10 — systemd user-unit hardening (swarm 3.4, added 2026-04-11)

**Decision**: Add the directives that **actually work** in user units; document why the rest are absent; explicitly prohibit `MemoryDenyWriteExecute` as a Go foot-gun.

**Finding from ArchWiki Systemd/Sandboxing**: "*Because of technical limitations, and ironically security reasons, user units can not be hardened or sandboxed properly since this would make privilege escalation issues possible.*" Mount-namespace directives (`ProtectSystem=strict`, `ProtectHome`, `PrivateTmp`, `PrivateDevices`, `RestrictNamespaces`) either silently no-op, fail, or degrade because the user-mode manager runs unprivileged and cannot set up mount namespaces without `CAP_SYS_ADMIN`.

**Directives that DO work in user units** (must be set):
- `NoNewPrivileges=yes` (explicit — not implicit in user mode)
- `LockPersonality=yes`
- `RestrictRealtime=yes`
- `RestrictSUIDSGID=yes`
- `SystemCallFilter=@system-service`
- `SystemCallArchitectures=native`

**Directives that MUST NOT be set**:
- `MemoryDenyWriteExecute=yes` — Go's garbage collector and stack management use writable-executable pages; enabling this causes the daemon to segfault at startup. Documented at [systemd#3814](https://github.com/systemd/systemd/issues/3814) and [linux-audit.com MDWE](https://linux-audit.com/systemd/settings/units/memorydenywriteexecute/).
- `ProtectSystem=strict`, `ProtectHome`, `PrivateDevices`, `PrivateTmp`, `RestrictNamespaces` — no-op or fail in user mode.
- `IPAddressDeny` — requires BPF controller delegation which is not available on most distros for user managers.

**launchd (darwin) corrections**:
- `KeepAlive` MUST be a dict `{Crashed: true, SuccessfulExit: false}`, not a bare bool. A clean `wa panic` exit does not respawn.
- `ProcessType = Background` is correct for a long-running non-UI daemon.
- `EnvironmentVariables.PATH` must be set explicitly — launchd gives children an empty PATH.
- `LimitLoadToSessionType` MUST NOT be set, so SSH-session invocations also work.
- Notarisation via `rcodesign` (feature 007) is on the critical path for the `curl`-installed tarball channel — binaries installed that way have the `com.apple.quarantine` xattr applied and will not run without notarisation.

**Sources**:
- [systemd.exec(5) man page](https://www.freedesktop.org/software/systemd/man/latest/systemd.exec.html)
- [ArchWiki Systemd/Sandboxing](https://wiki.archlinux.org/title/Systemd/Sandboxing)
- [Debian Wiki ServiceSandboxing](https://wiki.debian.org/ServiceSandboxing)
- [Linux Audit MemoryDenyWriteExecute](https://linux-audit.com/systemd/settings/units/memorydenywriteexecute/)
- [systemd#3814 MDWE fails Go services](https://github.com/systemd/systemd/issues/3814)
- [loginctl(1) man page](https://manpages.ubuntu.com/manpages/jammy/man1/loginctl.1.html)
- [launchd.plist(5)](https://keith.github.io/xcode-man-pages/launchd.plist.5.html)

---

## D11 — Name validation CVE posture (swarm 3.5, added 2026-04-11)

**Decision**: Regex is structurally sufficient, but layer `filepath.IsLocal` (Go 1.20+) as a defense-in-depth assertion at every path-join site, use `os.Root` (Go 1.24+) where possible, and mandate ANSI/control-char stripping on filesystem-sourced profile name output.

**Key findings**:

1. **`path/filepath.IsLocal`** (Go 1.20+) is a 4 ns lexical check that returns false for any name traversing out of its directory. Defense-in-depth over the regex: if a future maintainer relaxes the regex, IsLocal still catches traversal. See [go.dev/blog/osroot](https://go.dev/blog/osroot).

2. **`os.Root` / `os.OpenRoot`** (Go 1.24+) provides symlink-traversal-resistant file access. Use for the profile data directory. Note [CVE-2026-32282](https://github.com/golang/go/issues/78293) `Root.Chmod` symlink race fixed in Go 1.24.x via `fchmodat2` — `go 1.25` in `go.mod` is safe.

3. **Terminal escape injection in `profile list` output**: even though the regex rejects control characters, a profile directory can be created out-of-band with an arbitrary name. Output MUST strip `[\x00-\x1f\x7f\x80-\x9f]` and ANSI CSI sequences. Precedent: [CVE-2024-52005](https://www.cve.news/cve-2024-52005/) (Git sideband ANSI injection), CVE-2024-33899 (WinRAR hidden-file listing).

4. **Subcommand-verb reserved name additions**: the reserved name list must include the verbs that appear as `wa profile <verb>` — `list`, `use`, `create`, `rm`, `show`, `new`, `delete`, `current`, `switch`, `all`, `none`, `self`, `me`, `migrate`. A profile named `list` would make `wa profile list` ambiguous.

5. **systemd unit-type suffix words**: `service`, `socket`, `target`, etc. — hygiene matter even though the regex forbids `.`.

6. **Case-insensitive filesystem collision check**: on APFS/HFS+, `Work` and `work` are the same directory. `wa profile create` MUST check for existing differently-cased siblings before mkdir.

**Sources**:
- [Go blog — Traversal-resistant file APIs](https://go.dev/blog/osroot)
- [pkg.go.dev/path/filepath#IsLocal](https://pkg.go.dev/path/filepath#IsLocal)
- [CVE-2026-32282 / golang/go#78293](https://github.com/golang/go/issues/78293)
- [CVE-2024-52005 Git ANSI sideband](https://www.cve.news/cve-2024-52005/)
- [git check-ref-format docs](https://git-scm.com/docs/git-check-ref-format)
- [Windows naming-a-file](https://learn.microsoft.com/en-us/windows/win32/fileio/naming-a-file)
- [systemd USER_NAMES reserved list](https://systemd.io/USER_NAMES/)

---

## D8 — Pre-existing bug discovered during sweep

**Not strictly a decision, but a prerequisite**: During swarm 1.5 (constitution sweep), a pre-existing bug in `cmd/wad/main.go:122` was found: `SessionCreated: time.Now()` is hardcoded when constructing the dispatcher. This resets the warmup multiplier to "day 0" on every daemon restart, breaking the warmup ramp for single-profile users too.

**Fix**: Source `SessionCreated` from `sessionStore.Load(ctx).CreatedAt()`. When the session is zero (not yet paired), default to `time.Now()` and update once pairing completes.

**Scope**: Fix as a prerequisite task in feature 008, OR as a separate single-commit hotfix that ships before 008 implementation begins. Either way, it MUST land before 008 tests depend on accurate warmup timestamps.

---

## Summary of dependencies

No new Go module dependencies. The feature uses only:
- `github.com/adrg/xdg` (already in use, no profile awareness needed — we build our own path resolver on top)
- stdlib `filepath`, `os`, `os/exec`, `path/filepath`
- Existing cobra for CLI flags and completion
- Existing `text/template` for service file rendering

No new CGO. No new system dependencies. Purely a refactor + thin wrappers on top of existing plumbing.
