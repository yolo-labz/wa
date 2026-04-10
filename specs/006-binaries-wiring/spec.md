# Feature Specification: Binaries and Composition Root

**Feature Branch**: `006-binaries-wiring`
**Created**: 2026-04-09
**Status**: Draft
**Input**: User description: "Feature 006: Binaries and composition root. Build cmd/wad daemon binary wiring whatsmeow + sqlitestore + sqlitehistory + app.Dispatcher + socket.Server. Build cmd/wa thin CLI client with Cobra subcommands making JSON-RPC calls. Add allow and panic methods deferred from feature 005. Add signal.NotifyContext for graceful shutdown. First time wad actually runs end-to-end."

## Overview

This feature makes the daemon runnable. Features 002-005 delivered every layer of the hexagonal architecture — domain types, port interfaces, secondary adapters (whatsmeow, sqlitestore, sqlitehistory), primary adapter (socket server), and the application use case layer with its safety pipeline — but no binary existed to wire them together. Feature 006 adds the two binaries that live under `cmd/`: `wad` (the long-running daemon) and `wa` (the thin JSON-RPC client). It adds the small amount of glue code required to bridge layer boundaries that intentionally stayed at arm's length: the `dispatcherAdapter` that translates `app.Event` values to `socket.Event` values (per feature 005 research D2), and a small `Pairer` seam exposed by the whatsmeow adapter so the use case layer can invoke pairing without importing the adapter package. It also lands the two methods deferred from feature 005 — `allow` (allowlist mutation + `allowlist.toml` persistence) and `panic` (device unlink + session store clear) — because both require filesystem I/O and adapter interaction that only the composition root can wire.

At the end of this feature, a user can run `wad` on a clean machine, run `wa pair` in another terminal to bring up a QR code, scan it with their phone, and then run `wa send --to <jid> --body "hello"` to actually deliver a WhatsApp message through the daemon's safety pipeline. This is the first time the project becomes useful as a tool rather than as a library.

## User Scenarios & Testing *(mandatory)*

### User Story 1 — First-time pairing + send end-to-end (Priority: P1)

As a first-time user with a fresh machine, I run `wad` in one terminal. It prints a startup log, creates the unix socket at the XDG runtime path, and waits for clients. In another terminal I run `wa pair`, which prints a QR code in the terminal (half-block rendering). I scan the QR with WhatsApp on my phone. The daemon logs "paired ok", persists the session to the SQLite store, and the `wa pair` command returns success. I then run `wa allow add 5511999999999@s.whatsapp.net --actions send`, which adds the JID to the allowlist and persists the change to `allowlist.toml`. Finally I run `wa send --to 5511999999999@s.whatsapp.net --body "hello"`, which delivers the message. The whole flow works without any config file edits and without restarting the daemon between steps.

**Why this priority**: This is the **entire point** of the project. Every prior feature was scaffolding; this scenario is the first time the code does the thing the README promises.

**Independent Test**: A manual integration test on a developer machine with a burner WhatsApp number completes the pair → allow → send flow. An automated test using a fake whatsmeow client (reusing the `sockettest.FakeDispatcher` pattern) exercises the same JSON-RPC round-trips without network I/O.

**Acceptance Scenarios**:

1. **Given** a clean machine with no prior session, **When** the user runs `wad` and then `wa pair` in another terminal, **Then** a QR code appears in the pair terminal, scanning it with a phone returns `paired: true`, and the session is persisted to disk at `$XDG_DATA_HOME/wa/session.db`.
2. **Given** a paired daemon and an empty allowlist, **When** the user runs `wa send --to JID --body "hello"`, **Then** the command fails with exit code 11 (not-allowlisted) and a clear error message pointing the user at `wa allow add`.
3. **Given** a paired daemon and a JID added via `wa allow add JID --actions send`, **When** the user runs `wa send --to JID --body "hello"`, **Then** the message is delivered and the command returns exit code 0 with the message id on stdout.
4. **Given** a running daemon, **When** the user runs `wa status`, **Then** the command returns `{connected: true, jid: "..."}` in under 100 ms.
5. **Given** a running daemon, **When** the user presses `Ctrl-C` in the daemon terminal, **Then** the daemon drains in-flight requests, unlinks the socket, releases the file lock, and exits with status 0 within 2 seconds.

---

### User Story 2 — Allowlist mutation with persistent file (Priority: P1)

As a user managing which contacts the daemon can send to, I run `wa allow add <jid> --actions send,read` to grant permissions, `wa allow remove <jid>` to revoke them, and `wa allow list` to see current state. Every mutation is atomically written to `$XDG_CONFIG_HOME/wa/allowlist.toml` and hot-reloaded by the running daemon without restart. The file is human-readable so a user can also edit it by hand and send `SIGHUP` to the daemon (or wait for the daemon's file watcher) to apply the changes.

**Why this priority**: Without a way to mutate the allowlist at runtime, the daemon is useless after the first send. The constitution's "default deny" principle means the allowlist is the primary configuration surface.

**Independent Test**: A test spins up a daemon, calls `allow add` via the socket, then reads `allowlist.toml` from disk and asserts the new entry is present. A second test calls `allow remove` and asserts the entry is gone. A third test writes the TOML file by hand, sends a signal (or waits for reload), and asserts the new state is live.

**Acceptance Scenarios**:

1. **Given** an empty allowlist, **When** the user runs `wa allow add 5511@s.whatsapp.net --actions send`, **Then** `allowlist.toml` contains a `[[rules]]` entry with that JID and those actions, and a subsequent `wa send` to that JID succeeds.
2. **Given** a JID with `send` action, **When** the user runs `wa allow remove 5511@s.whatsapp.net`, **Then** the entry is removed from the TOML and subsequent `wa send` to that JID returns not-allowlisted.
3. **Given** an edited `allowlist.toml`, **When** the daemon reloads (signal or watcher), **Then** the in-memory allowlist reflects the file contents within 1 second without any requests in flight being dropped.
4. **Given** a malformed TOML file, **When** the daemon attempts to reload, **Then** the daemon keeps the previous valid allowlist and logs an error without crashing.

---

### User Story 3 — Device unlinking and session wipe (Priority: P1)

As a user who wants to re-pair with a different phone, or who suspects their session has been compromised, I run `wa panic`. The daemon unlinks the device server-side (whatsmeow's `Client.Logout`), wipes the local session store (`session.db` removed), and the next `wa pair` starts a fresh QR flow. The panic command requires no confirmation — the name is the warning — and always succeeds even if the upstream unlink call fails (the local wipe still happens).

**Why this priority**: Security feature. Anyone who has physical access to the phone can produce a surprise and the user needs a one-shot recovery action.

**Independent Test**: A test pairs a fake session, calls `wa panic`, and asserts (a) the session store file no longer exists on disk, (b) the in-memory session handle is cleared, (c) the next `wa pair` starts a fresh QR flow, (d) the daemon logged the panic event to the audit log.

**Acceptance Scenarios**:

1. **Given** a paired daemon, **When** the user runs `wa panic`, **Then** the command returns `{unlinked: true}` and the daemon logs an `AuditPanic` entry.
2. **Given** a panic that succeeded, **When** the user runs `wa pair`, **Then** a fresh QR flow begins (no "already paired" error).
3. **Given** a panic that failed to reach the upstream server (network down), **When** the user re-runs `wa panic`, **Then** the command still succeeds because the local wipe is the primary effect.

---

### User Story 4 — Status and observability (Priority: P2)

As a user or operator debugging the daemon, I run `wa status` to see connection state + paired JID, `wa groups` to list joined groups, and `wa wait --events message --timeout 30s` to block on an inbound message. The daemon's log output is `slog` JSON structured, so I can pipe it through `jq` to filter, and the audit log is a separate append-only file at `$XDG_STATE_HOME/wa/audit.log`.

**Why this priority**: Essential for debugging but not blocking for the core pair-and-send flow.

**Independent Test**: Each status/groups/wait command is tested via the socket adapter with the fake dispatcher (already covered by feature 004/005 tests). This feature only adds the CLI glue.

**Acceptance Scenarios**:

1. **Given** a running daemon, **When** the user runs `wa status --json`, **Then** stdout is a single JSON object with `schema: "wa.status/v1"` + fields `connected`, `jid`, `lastEvent`.
2. **Given** a user with 3 joined groups, **When** they run `wa groups`, **Then** stdout shows a human-readable table (or JSON with `--json`) listing all 3 groups.
3. **Given** a daemon ready to receive, **When** the user runs `wa wait --events message --timeout 30s` and another device sends a message, **Then** the command returns within 30 seconds with the message payload on stdout and exit code 0. If no message arrives, exit code 12 (wait-timeout).

---

### User Story 5 — Graceful shutdown via signals (Priority: P1)

As the operator running the daemon under launchd or systemd, when the service manager sends `SIGTERM`, the daemon drains in-flight JSON-RPC requests up to the configured deadline, sends final shutdown notifications to subscribing clients, unlinks the socket file, releases the advisory lock, closes the whatsmeow websocket cleanly, and exits with status 0. The same happens for `SIGINT` (Ctrl-C). The daemon never leaves a stale socket file behind after a clean shutdown, and on unclean kill (`SIGKILL`) the next startup detects the stale socket via the lock file and unlinks it safely.

**Why this priority**: A daemon that doesn't honor its service manager contract is broken. launchd and systemd both send `SIGTERM` before `SIGKILL`; if the daemon hangs, the session store can corrupt (feature 003's whatsmeow adapter documents this risk).

**Independent Test**: A test starts a daemon, sends `SIGTERM` in-process via `signal.NotifyContext`, asserts `wad.Run(ctx)` returns within 2 seconds, asserts the socket file is gone and the lock is released, asserts a new daemon can start immediately on the same path.

**Acceptance Scenarios**:

1. **Given** a running daemon with zero in-flight requests, **When** `SIGTERM` is delivered, **Then** the daemon exits within 2 seconds with status 0 and the socket file is unlinked.
2. **Given** a running daemon with 3 in-flight slow requests, **When** `SIGTERM` is delivered, **Then** all 3 requests receive responses (or documented drain-deadline errors) before the daemon exits, within 6 seconds total.
3. **Given** a daemon killed by `SIGKILL`, **When** a new daemon starts on the same socket path, **Then** the new daemon detects the stale socket via the lock file, unlinks it, and starts successfully.
4. **Given** a running daemon, **When** the user presses `Ctrl-C` (`SIGINT`), **Then** the behavior is identical to `SIGTERM`.
5. **Given** a `wa pair` QR flow in progress, **When** `SIGTERM` is delivered to the daemon, **Then** the pairing context is cancelled, the QR flow aborts, the `wa pair` client receives a shutdown error, and the daemon still exits cleanly within the 10-second deadline.

---

### User Story 6 — Human-friendly CLI output (Priority: P2)

As a user, when I run `wa send --to JID --body "hello"` without `--json`, the output is a single human-readable line like `sent to 5511...@s.whatsapp.net at 15:04:05 (id: 3EB0...)`. With `--json`, the output is a versioned NDJSON line `{"schema":"wa.send/v1","messageId":"...","timestamp":1234567890}`. Exit codes follow the sysexits pattern defined in CLAUDE.md: 0 ok, 64 usage, 10 not-paired, 11 not-allowlisted, 12 rate-limited/wait-timeout, 78 config error. The root `wa` command supports `--help`, `--version`, `--socket <path>`, and `--verbose`.

**Why this priority**: Usability matters for a CLI tool but the core value (send + pair) works without polish.

**Independent Test**: Each subcommand is tested with `rogpeppe/go-internal/testscript` golden files that pin stdout, stderr, and exit code for both success and error cases.

**Acceptance Scenarios**:

1. **Given** a successful send, **When** invoked without `--json`, **Then** stdout contains exactly one line matching `sent to <jid> at <time> (id: <id>)`.
2. **Given** a successful send, **When** invoked with `--json`, **Then** stdout contains exactly one NDJSON line with fields `schema`, `messageId`, `timestamp`.
3. **Given** a not-allowlisted error, **When** invoked without `--json`, **Then** stderr contains a clear error message and exit code is 11.
4. **Given** the daemon is not running, **When** any `wa` command is invoked, **Then** exit code is 10 (service-unavailable) and stderr points at `wad` startup instructions.

---

### Edge Cases

- **First startup — parent directories do not exist**: `wad` MUST create `$XDG_DATA_HOME/wa/`, `$XDG_CONFIG_HOME/wa/`, `$XDG_STATE_HOME/wa/`, and `$XDG_RUNTIME_DIR/wa/` with mode `0700` on first start, propagating errors clearly.
- **First startup — no allowlist file**: The daemon starts with an empty allowlist (default deny). Every send fails with a clear "run wa allow add" hint.
- **`wa` command run when `wad` is not running**: The client returns exit code 10 (service-unavailable) and stderr points at `wad`.
- **`wa` command run from a different user's session**: The peer-credential check at the socket rejects the connection; `wa` surfaces a clear "access denied" error.
- **Allowlist TOML with a syntactically valid but semantically unknown field**: The parser ignores unknown fields (forward compat) and logs a WARN entry naming the field.
- **`wa pair` while session already exists**: Returns exit code 10 + a clear message. The user must run `wa panic` to re-pair.
- **`wa panic` while `wad` is not running**: The command refuses with exit code 10 — the daemon is the only process that owns the session store.
- **`wad` crashes mid-request**: The client's JSON-RPC call returns a connection-reset error with exit code 10.
- **`wad` `SIGTERM` during `wa pair`**: The QR flow is cancelled, the pairing context is cancelled, and the client returns a shutdown error.
- **Concurrent `wa` commands**: Multiple `wa` clients can connect simultaneously; the socket adapter handles at least 16 concurrent connections (per feature 004 FR-027). Rate-limiter state is shared across all of them.

## Requirements *(mandatory)*

### Functional Requirements

#### `wad` composition root

- **FR-001**: The `wad` binary MUST live at `cmd/wad/main.go` and MUST NOT contain any use case logic beyond wiring — only dependency construction and lifecycle management.
- **FR-002**: On startup, `wad` MUST construct secondary adapters in order: `sqlitestore` (session DB), `sqlitehistory` (messages DB), `slogaudit` (audit log), `whatsmeow` adapter (holding the first three). It MUST then construct the `app.Dispatcher` use case layer, then the `socket.Server` primary adapter, then call `Server.Run(ctx)`.
- **FR-003**: `wad` MUST use `signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)` to derive the root context. The context is cancelled when either signal is received.
- **FR-004**: `wad` MUST contain a thin `dispatcherAdapter` type that satisfies the `socket.Dispatcher` interface by delegating `Handle` to `app.Dispatcher.Handle` and converting the `<-chan app.Event` from `app.Dispatcher.Events()` into a `<-chan socket.Event` via a goroutine.
- **FR-005**: `wad` MUST exit with a non-zero status and a clear error message if any adapter construction fails (missing directories, corrupt session DB, locked session DB, etc.).
- **FR-006**: `wad` MUST create `$XDG_DATA_HOME/wa/`, `$XDG_CONFIG_HOME/wa/`, `$XDG_STATE_HOME/wa/`, and `$XDG_RUNTIME_DIR/wa/` with mode `0700` if they do not exist. On macOS, the runtime dir resolves per feature 004's `path_darwin.go` (`~/Library/Caches/wa/`).
- **FR-007**: `wad` MUST exit cleanly on `SIGTERM` or `SIGINT`: drain in-flight requests up to the socket server's configured deadline, close the whatsmeow websocket, close the sqlite stores, unlink the socket, release the lock, return from `main` within 10 seconds worst case.
- **FR-008**: `wad` MUST emit structured logs via `slog` to stderr in JSON format. Log level defaults to INFO, configurable via `--log-level` or `WA_LOG_LEVEL` env var.

#### `wa` CLI client

- **FR-009**: The `wa` binary MUST live at `cmd/wa/main.go` and MUST contain ZERO use case logic — every subcommand MUST be implemented as a JSON-RPC call against `wad` over the unix socket. `cmd/wa` MUST NOT import `internal/domain`, `internal/app`, or any adapter package EXCEPT `internal/adapters/primary/socket` for the `socket.Path()` function (the socket path resolver). This single exception is permitted because the client needs to know where to dial; it does not import any business logic.
- **FR-010**: `wa` MUST use `spf13/cobra` for the subcommand tree and `charmbracelet/fang` for styling per CLAUDE.md §Decisions.
- **FR-011**: `wa` MUST expose these subcommands: `pair`, `status`, `send`, `sendMedia`, `react`, `markRead`, `groups`, `wait`, `allow [add|remove|list]`, `panic`, `version`. Global flags: `--socket <path>`, `--json`, `--verbose`, `--help`, `--version`.
- **FR-012**: `wa` MUST resolve the socket path using feature 004's `socket.Path()` function. The `--socket` flag overrides the default.
- **FR-013**: Every subcommand MUST produce human-readable output by default and NDJSON output when `--json` is passed. NDJSON lines MUST include a `schema` field of the form `wa.<method>/v1`. Human output formats: `send` → `sent to <jid> at <HH:MM:SS> (id: <id>)`; `status` → `connected: yes, jid: <jid>` or `connected: no`; `groups` → tabular `JID  SUBJECT  MEMBERS` per line; `pair` → `paired ok` or the QR code; `allow list` → tabular `JID  ACTIONS` per line.
- **FR-014**: `wa` MUST exit with sysexits-conformant codes per CLAUDE.md §Output schema: 0 (ok), 64 (usage error), 10 (service unavailable / not paired), 11 (not allowlisted), 12 (rate limited or wait timeout), 78 (config error).
- **FR-015**: `wa` MUST translate JSON-RPC error codes to exit codes via a documented table in `cmd/wa/exitcodes.go`: `-32011` → 10, `-32012` → 11, `-32013` → 12, `-32014` → 12, `-32015` → 64, `-32602` → 64, connection refused → 10.

#### `allow` method (deferred from feature 005)

- **FR-016**: The `allow` method MUST parse params `{op: "add"|"remove"|"list", jid?: string, actions?: [string]}`.
- **FR-017**: On `op: "add"`, the daemon MUST add a rule `{jid, actions}` to the in-memory allowlist and atomically persist the full allowlist to `$XDG_CONFIG_HOME/wa/allowlist.toml` using a write-then-rename pattern to prevent partial writes.
- **FR-018**: On `op: "remove"`, the daemon MUST remove all rules for the specified JID and persist the updated file.
- **FR-019**: On `op: "list"`, the daemon MUST return the full current allowlist state as a JSON object without touching the file.
- **FR-020**: The allowlist persistence format MUST be human-readable TOML with a schema documented in `contracts/allowlist-toml.md`.
- **FR-021**: The daemon MUST reload the allowlist from `allowlist.toml` on startup. If the file does not exist, the daemon starts with an empty allowlist (default deny).
- **FR-022**: The daemon MUST reload the allowlist from the file when it receives `SIGHUP`, OR when a file watcher (fsnotify) detects the file has changed. Both mechanisms MUST work; the watcher is the primary path.
- **FR-023**: If the TOML file is malformed, the daemon MUST log an ERROR, keep the previous valid in-memory allowlist, and NOT crash.
- **FR-024**: `allow add` and `allow remove` MUST produce audit log entries (`AuditGrant`, `AuditRevoke`). `allow list` MUST NOT.

#### `panic` method (deferred from feature 005)

- **FR-025**: The `panic` method MUST take no parameters and MUST unlink the device server-side by calling the whatsmeow adapter's logout capability, then clear the session store via `SessionStore.Clear`.
- **FR-026**: The `panic` method MUST always succeed locally even if the server-side unlink fails — the local wipe is the primary effect and is recorded unconditionally.
- **FR-027**: The `panic` method MUST produce an audit log entry with action `AuditPanic` and outcome "unlinked" or "unlinked:local-only" depending on whether the server-side call succeeded.
- **FR-028**: After `panic`, the daemon's in-memory state MUST reflect "not paired" — subsequent `wa send` calls MUST return not-paired errors and `wa pair` MUST start a fresh QR flow without "already paired" rejection.

#### Pairer seam

- **FR-029**: The whatsmeow adapter MUST expose a `Pairer` interface (either as a new port in `internal/app/ports.go` OR as a method on the existing adapter type that the composition root uses directly) that the app layer consumes to invoke pairing without importing the whatsmeow package.
- **FR-030**: The `Pairer` interface MUST support both QR and phone-code flows, matching the shape used internally by feature 003's adapter.

#### Graceful shutdown

- **FR-031**: `wad` MUST use `signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)` as the root context.
- **FR-032**: On context cancellation, `wad` MUST call `socket.Server.Shutdown()` then `socket.Server.Wait()` then close all adapters in reverse construction order.
- **FR-033**: Adapter close order: `socket.Server` (stops new accepts) → `app.Dispatcher.Close()` (drains event bridge) → `whatsmeow.Adapter.Close()` (closes websocket) → `sqlitehistory.Close()` → `sqlitestore.Close()`.
- **FR-034**: `wad` MUST log each shutdown step at INFO level so an operator can diagnose a hung shutdown.
- **FR-035**: Total shutdown deadline is 10 seconds. If any adapter close hangs past 10 seconds, `wad` logs an ERROR and exits non-zero.

#### End-to-end integration test

- **FR-036**: The feature MUST include an integration test at `cmd/wad/integration_test.go` gated behind `//go:build integration` and `WA_INTEGRATION=1` that: starts `wad` in-process, constructs a test client, exercises `pair → allow add → send` against a fake whatsmeow, and asserts the full pipeline works.
- **FR-037**: A second automated test at `cmd/wa/cli_test.go` uses `rogpeppe/go-internal/testscript` to exercise each subcommand against a fake daemon and asserts stdout, stderr, and exit code match golden files.

### Key Entities

- **`wad` main** — the composition root binary. Holds references to every adapter and layer; no business logic.
- **`dispatcherAdapter`** — the thin bridge type in `cmd/wad` that satisfies `socket.Dispatcher` by delegating to `app.Dispatcher` and converting event channels (per feature 005 research D2).
- **`wa` main** — the CLI client binary. Cobra root + subcommand tree + JSON-RPC transport.
- **`allowlistFile`** — the TOML persistence layer for the allowlist. Read on startup, written on mutation, reloaded on signal or file change.
- **`Pairer`** — the seam between the use case layer (`internal/app/`) and the whatsmeow pairing capability. Either a new port interface or a concrete type the composition root consumes directly.
- **`exitCodeMap`** — the table in `cmd/wa/exitcodes.go` mapping JSON-RPC error codes to sysexits exit codes.

## Success Criteria *(mandatory)*

### Measurable Outcomes

- **SC-001**: On a clean machine, running `wad` + `wa pair` + (scan QR) + `wa allow add <jid> --actions send` + `wa send --to <jid> --body "hello"` delivers a WhatsApp message end-to-end in under 2 minutes (dominated by the manual QR scan).
- **SC-002**: `wa status` returns in under 100 ms against a running daemon, measured from shell invocation to exit.
- **SC-003**: `wad` cold startup (first time ever, creating all directories) completes in under 500 ms excluding the whatsmeow websocket connection.
- **SC-004**: `wad` graceful shutdown completes in under 2 seconds with zero in-flight requests, measured from `SIGTERM` delivery to process exit.
- **SC-005**: The `allow add` → TOML write → file watcher reload cycle completes in under 1 second end to end, verified by a test that writes the file and then calls `wa send` immediately.
- **SC-006**: `wa panic` completes in under 500 ms and leaves no session files on disk.
- **SC-007**: The integration test at `cmd/wad/integration_test.go` exercises pair → allow → send with a fake whatsmeow client and passes with `-race` in under 5 seconds.
- **SC-008**: Every subcommand has a golden-file testscript covering success and at least one failure path.
- **SC-009**: `go test -race ./...` across the whole repo passes in under 60 seconds wall clock.
- **SC-010**: `golangci-lint run ./...` reports zero findings, including the `app-no-adapters` and `core-no-whatsmeow` depguard rules.

## Assumptions

- The manual end-to-end test with a real WhatsApp burner phone is a manual operation performed by the maintainer, not a CI job. CI runs the fake-whatsmeow integration test only.
- The `wa` binary does not ship with bash completion in v0.1. Cobra supports it natively but it is deferred to feature 007's packaging work.
- The file watcher for `allowlist.toml` uses `fsnotify` (or equivalent). If the platform does not support inotify/kqueue (unlikely on Linux/macOS), the daemon falls back to `SIGHUP`-only reload.
- The `wa` client does not cache anything locally. Every invocation opens a fresh connection, calls one method, and exits. There is no persistent client state.
- The `Pairer` seam is the ONLY cross-feature change to `internal/app/ports.go` in this feature. No other port changes.
- The `dispatcherAdapter` in `cmd/wad` is ~20 LoC per feature 005 research D2. It is the only place where `app.Event` and `socket.Event` meet.
- `cmd/wad` and `cmd/wa` depend on features 002–005. They are the leaf of the dependency graph.
- The audit log path uses `$XDG_STATE_HOME/wa/audit.log` per CLAUDE.md §FS layout.

## Dependencies

- **Feature 002 (domain-and-ports)** — domain types and port interfaces; adds `Pairer` interface (one new port).
- **Feature 003 (whatsmeow-adapter)** — secondary adapter; exposes the `Pairer` seam.
- **Feature 004 (socket-adapter)** — primary adapter; consumed via `socket.Server` and `socket.Dispatcher` interface.
- **Feature 005 (app-usecases)** — use case layer; consumed via `app.Dispatcher`.

## Out of Scope

- GoReleaser pipeline, launchd/systemd unit files, Homebrew tap, Nix flake — all deferred to feature 007.
- macOS notarization via rcodesign — feature 007.
- Bash/zsh completion scripts — feature 007.
- `wa doctor` diagnostic subcommand — deferred past v0.1.
- Multi-profile support (`config.toml` `[profile.work]` sections) — deferred past v0.1.
- In-process self-update (`wa upgrade`) — feature 007 replaces this with "print the brew/nix upgrade command."
- Metrics/Prometheus endpoint — deferred past v0.1.
- Windows support — not targeted in v0.1 per CLAUDE.md.
