# Feature Specification: whatsmeow Secondary Adapter

**Feature Branch**: `003-whatsmeow-adapter`
**Created**: 2026-04-07
**Status**: Draft
**Input**: User description: "003 — whatsmeow secondary adapter"

## User Scenarios & Testing *(mandatory)*

### User Story 1 - The same use cases run against a real WhatsApp account (Priority: P1)

A maintainer working on feature 004 (the daemon composition root) needs to swap the in-memory adapter from feature 002 for a real WhatsApp adapter without changing any file under `internal/domain/` or `internal/app/`. Every use case the in-memory adapter satisfied must now satisfy itself against an actual paired WhatsApp account: sending a message produces a real `MessageID`, listing groups returns the user's real groups, querying a contact returns the contact whatsmeow knows about, the inbound event stream surfaces real receipts and connection state changes.

**Why this priority**: This is the test of whether the hexagonal layering from feature 002 was real or theatre. If feature 003 has to modify a single byte under `internal/domain/` or `internal/app/`, the architecture failed and every later feature pays the cost forever. Conversely, if feature 003 lands cleanly, every future adapter (Cloud API, REST gateway, mock for fuzzing) inherits the same property.

**Independent Test**: With a paired WhatsApp burner number, run `go test -race -tags integration ./internal/adapters/secondary/whatsmeow/...` (gated by `WA_INTEGRATION=1`) and observe the contract test suite from `internal/app/porttest/` pass against the whatsmeow adapter exactly as it passes against the in-memory adapter from feature 002. Then run `git diff main..003-whatsmeow-adapter -- internal/domain internal/app/ports.go` and observe zero output.

**Acceptance Scenarios**:

1. **Given** a paired WhatsApp number, **When** a use case calls `MessageSender.Send` with a `domain.TextMessage` whose `Recipient` is a known contact, **Then** the real WhatsApp servers acknowledge the send and the adapter returns a non-zero `domain.MessageID` plus a nil error, with the message visible in the recipient's chat history.
2. **Given** a paired WhatsApp number, **When** a use case calls `EventStream.Next(ctx)` with a 30-second deadline, **Then** the adapter returns the next real inbound event from the WhatsApp websocket within the deadline (or the deadline cancels cleanly).
3. **Given** the same use case wired against the in-memory adapter and against the whatsmeow adapter, **When** the use case is exercised with identical inputs, **Then** both adapters produce structurally identical observable behaviour at the port boundary (modulo the `MessageID` value, which is opaque).
4. **Given** an attempt to import `go.mau.fi/whatsmeow` from any file under `internal/domain/` or `internal/app/`, **When** `golangci-lint run` executes the `core-no-whatsmeow` rule, **Then** the build fails with the rule's deny message naming the file.

---

### User Story 2 - The user can pair a WhatsApp number from a terminal (Priority: P1)

A maintainer needs to take a fresh copy of the project and a phone running WhatsApp, run a single command, scan a QR code (or type a phone-pairing code), and have the project's session store hold a valid linked-device identity that survives restarts. Subsequent process launches reuse the existing session without prompting.

**Why this priority**: A perfectly hexagonal adapter that cannot pair is useless. Pairing is the load-bearing UX moment of the entire project — it is the only time a human is in the loop, the only operation that requires synchronous coordination between the daemon, the user's terminal, and a phone — and the spec for `Pair` was deferred from feature 002 because it inherently requires whatsmeow.

**Independent Test**: On a fresh clone with no `~/.local/share/wa/session.db`, run the pairing harness under `internal/adapters/secondary/whatsmeow/internal/pairtest/` (gated by `WA_INTEGRATION=1`), scan the half-block QR code printed to stderr, observe the harness exit 0 and the session.db file appear at the documented path. Re-run the harness and observe that no QR is printed and the existing session is reused.

**Acceptance Scenarios**:

1. **Given** an empty session store, **When** the maintainer runs the pairing harness without arguments, **Then** the harness prints a half-block QR code to stderr (SSH-safe), waits for the phone to scan it, and exits 0 within 5 minutes.
2. **Given** an empty session store, **When** the maintainer runs the pairing harness with `--phone +5511999999999`, **Then** the harness prints an 8-character pairing code (formatted `XXXX-XXXX`) and instructions for entering it under WhatsApp → Settings → Linked Devices → Link with phone number, and exits 0 once the code is consumed.
3. **Given** an existing valid session, **When** the harness runs again, **Then** it does NOT print a QR or pairing code; it reuses the existing session and connects to the websocket within 3 seconds.
4. **Given** a session that the WhatsApp server has invalidated (events.LoggedOut), **When** the daemon-side adapter detects it, **Then** the adapter clears its local SessionStore, surfaces a `domain.PairingEvent` with state `PairFailure` to the EventStream, and waits for the next manual pairing call instead of attempting an automatic re-pair.

---

### User Story 3 - The session ratchet store survives restarts and is single-instance (Priority: P1)

A maintainer running the daemon needs to be confident that (a) the SQLite session database persists across process restarts without losing key material, (b) two daemon instances can never write to the same database file simultaneously, and (c) the database file lives at the documented XDG path with the correct file permissions.

**Why this priority**: whatsmeow's SQLite store does not lock — two writers corrupt the Signal Protocol ratchet, which means losing the device identity and forcing a re-pair. The `flock` enforcement is therefore the difference between a working installation and silent data corruption.

**Independent Test**: Start one instance of the pairing harness and pair successfully. Start a second instance of the same harness in another terminal without killing the first; observe the second instance fail immediately with a typed "session locked" error and exit code 11 (or equivalent), without touching the database file. Kill the first instance and confirm the second can now start cleanly.

**Acceptance Scenarios**:

1. **Given** no daemon is running, **When** the first instance starts, **Then** it acquires an `flock(LOCK_EX|LOCK_NB)` on the SQLite file path, opens the database, and proceeds.
2. **Given** the first instance is running, **When** a second instance attempts to start, **Then** it fails to acquire the lock and exits with a typed error wrapping "session locked"; the database file is unchanged.
3. **Given** the first instance crashes (kill -9), **When** the OS releases the file lock, **Then** the second instance can start cleanly without any manual cleanup.
4. **Given** a freshly created database, **When** the maintainer inspects the file permissions, **Then** the file is `chmod 0600` and the parent directory is `chmod 0700`, matching the spec in CLAUDE.md §"Filesystem layout".

---

### User Story 4 - whatsmeow protocol bumps do not break the build silently (Priority: P2)

A maintainer wants the project's behaviour pinned against a known whatsmeow commit, with each Renovate-driven bump producing a CI run that exercises the contract suite and surfaces the upstream commit range in the PR description, so that protocol-breaking changes are caught the moment they land in `go.sum` rather than the moment a real message fails to send weeks later.

**Why this priority**: whatsmeow has no semver tags. Every bump is a pseudo-version pointing at a commit hash. Without an automated re-validation loop, bumps either fail silently in production or stop happening. This story is the operational backstop for the architectural invariant from US1.

**Independent Test**: Manually merge a Renovate PR that bumps `go.mau.fi/whatsmeow` to a newer commit, observe the CI workflow run the contract suite against the new whatsmeow version and either pass (the protocol is still compatible) or fail with the offending port method named in the test output.

**Acceptance Scenarios**:

1. **Given** the existing `renovate.json` `whatsmeow` package rule, **When** Renovate opens a bump PR, **Then** the PR body contains the upstream commit range (per `fetchChangeLogs: branch`) and the CI workflow runs `go test ./...` plus `golangci-lint run ./...`.
2. **Given** a whatsmeow bump that breaks an event-translation assumption, **When** CI runs, **Then** the contract test suite reports the failing clause by name and the PR cannot be merged until the secondary adapter is updated.
3. **Given** a maintainer manually downgrading whatsmeow to investigate a regression, **When** they pin a previous pseudo-version in `go.mod` and run `go test`, **Then** the contract suite passes or fails deterministically against that version.

---

### Edge Cases

- **Laptop sleep mid-session**: whatsmeow's built-in reconnect loop attempts to reconnect. The adapter surfaces `events.Disconnected` and `events.Connected` to the `EventStream` as `domain.ConnectionEvent` values with monotonic `EventID`s, so a `wa status` consumer can detect transitions even across long sleeps. No state is lost; the consumer's job is to drain the events at its own pace.
- **WhatsApp invalidates the session** (e.g., user manually unlinked the device from their phone): the adapter detects `events.LoggedOut`, clears the local `SessionStore` via `Clear`, emits a `PairingEvent` with state `PairFailure`, and refuses subsequent `Send` calls with a typed error wrapping `domain.ErrInvalidJID` (or a new sentinel if needed) until a fresh pair completes.
- **`MediaMessage.Path` no longer exists on disk**: the adapter `os.Stat`s the path, returns `fmt.Errorf("%w: %s", domain.ErrEmptyBody, path)` with the file-not-found indicator chained, and never contacts WhatsApp.
- **Inbound event references a contact JID the adapter has never seen**: the adapter returns the JID as-is from `EventStream.Next`; the `ContactDirectory.Lookup` for that JID returns a typed "not found" error so the consumer can decide whether to ignore the event, fetch via a separate path, or fail.
- **whatsmeow's `AddEventHandler` callback panics**: the adapter wraps the handler in a recovery goroutine that logs the panic to the audit log via `AuditLog.Record` (with `AuditAction.AuditPanic`) and continues serving the buffered `EventStream`. A panic in the handler MUST NOT take down the daemon.
- **Phone has no internet for an extended period**: whatsmeow buffers messages server-side; on reconnect, history sync delivers them. The adapter surfaces them through the normal `EventStream`. Volume governed by FR-019 / OPEN-Q2 below.

## Requirements *(mandatory)*

### Functional Requirements

- **FR-001**: The repository MUST contain a package at `internal/adapters/secondary/whatsmeow/` whose exported `Adapter` (or equivalent) struct implements every port interface declared in `internal/app/ports.go` from feature 002.
- **FR-002**: The `Adapter` MUST be the only place in this feature where `go.mau.fi/whatsmeow` and its subpackages are imported. The `core-no-whatsmeow` `depguard` rule MUST continue to forbid those imports under `internal/domain/` and `internal/app/`, and zero domain or app file may be modified by this feature beyond cosmetic doc-comment additions.
- **FR-003**: The `Adapter` MUST translate between `whatsmeow/types.JID` and `domain.JID` at every port boundary, with no `whatsmeow/types.JID` value escaping the `internal/adapters/secondary/whatsmeow/` package.
- **FR-004**: The `Adapter` MUST translate inbound `events.Message`, `events.Receipt`, `events.Connected`, `events.Disconnected`, `events.LoggedOut`, `events.PairSuccess`, `events.PairError`, and `events.QR` into the corresponding `domain.MessageEvent`, `domain.ReceiptEvent`, `domain.ConnectionEvent`, and `domain.PairingEvent` variants, with monotonically-increasing `domain.EventID` values assigned at translation time.
- **FR-005**: The repository MUST contain a package at `internal/adapters/secondary/sqlitestore/` that wraps `whatsmeow/sqlstore` and exposes a small constructor returning a `*sqlstore.Container` ready for use by the `Adapter`. This package is the only place in the project that imports `whatsmeow/sqlstore` and the SQLite driver.
- **FR-006**: The session database MUST live at `$XDG_DATA_HOME/wa/session.db` (with the `adrg/xdg` macOS fallback) and MUST be opened with file permissions `0600` and parent directory permissions `0700`.
- **FR-007**: The database file MUST be guarded by `flock(LOCK_EX|LOCK_NB)` at adapter startup. A failed lock acquisition MUST return a typed error wrapping a "session locked" indicator and the adapter MUST NOT touch the database file in any other way.
- **FR-008**: Pairing MUST default to QR-in-terminal using `mdp/qrterminal/v3 GenerateHalfBlock` written to `os.Stderr`. Phone-pairing-code flow MUST be available as an opt-in via a `Pair(ctx, phoneE164 string)` constructor argument or equivalent. Both flows MUST use a context derived from `context.Background()` with a 3-minute timeout, NOT a request context, to prevent the aldinokemal-style mid-pair cancellation bug.
- **FR-009**: The whatsmeow `*Client` MUST be configured with the production flags from `mautrix/whatsapp/pkg/connector/client.go`: `AddEventHandlerWithSuccessStatus`, `SynchronousAck = true`, `EnableDecryptedEventBuffer`, `ManualHistorySyncDownload = true`, `SendReportingTokens = true`, `AutomaticMessageRerequestFromPhone = true`, `InitialAutoReconnect`, and `UseRetryMessageStore`. The constants and their values MUST be documented in this feature's `data-model.md`.
- **FR-010**: On `events.LoggedOut`, the adapter MUST clear its local session store via `SessionStore.Clear`, emit a `PairingEvent{State: PairFailure}` to the `EventStream`, and refuse subsequent outbound calls until a new pair completes.
- **FR-011**: The adapter's `EventStream.Next` MUST be implemented over an internal bounded channel (≥100 capacity) fed by whatsmeow's event handler. `Next` MUST honour `ctx.Done()` cancellation and MUST NOT busy-wait. `Ack` MUST be a no-op for the in-process adapter (the daemon's persistence layer is feature 004's concern).
- **FR-012**: The adapter MUST use a **single long-lived `clientCtx`** derived from `context.Background()` for the underlying `*whatsmeow.Client`'s lifetime, cancelled only on `Adapter.Close`. Per-request contexts MUST NOT be passed to the whatsmeow client itself; they only cancel waiting operations within the adapter.
- **FR-013**: The contract test suite at `internal/app/porttest/` from feature 002 MUST be runnable against the new adapter via a single test file `internal/adapters/secondary/whatsmeow/adapter_integration_test.go` gated by `//go:build integration` and `WA_INTEGRATION=1`. It MUST use the existing `porttest.RunContractSuite(t, factory)` entrypoint with no modifications to `porttest/`.
- **FR-014**: Unit tests for the JID translator, the event translator, the file-permission setter, and the flock guard MUST run unconditionally (no build tag, no environment variable) and MUST NOT touch a real WhatsApp account.
- **FR-015**: The `whatsmeow` Renovate package rule MUST remain active and MUST surface every bump as a separate PR per the configuration in `renovate.json`.
- **FR-016**: The feature MUST NOT introduce a daemon process, a CLI binary, a JSON-RPC socket server, a rate limiter, an audit log file writer, an allowlist file watcher, or any Claude Code plugin code. Those belong to features 004, 005, and 007 respectively.
- **FR-017**: The feature MUST NOT modify any file under `internal/domain/` or `internal/app/ports.go` except via separate commits explicitly named in `tasks.md`. The depguard rule continues to enforce the import boundary; this requirement extends it to any structural change.
- **FR-018**: Outbound `MessageSender.Send` calls during a disconnected adapter state [NEEDS CLARIFICATION: should they queue locally and replay on reconnect, or fail immediately with a typed error so the caller can decide?]
- **FR-019**: History sync on first connect [NEEDS CLARIFICATION: should v0 enable whatsmeow's full history sync (~2GB+ for active accounts) or skip it entirely (zero history at first connect, build state from new events only)?]
- **FR-020**: The adapter's exposure of `events.HistorySync` to the `EventStream` port [NEEDS CLARIFICATION: should bulk history events be surfaced through the same `Next()` channel as live events, or consumed internally and translated into per-conversation `MessageEvent`s only when the consumer asks?]

### Key Entities

- **whatsmeow Adapter**: A Go struct under `internal/adapters/secondary/whatsmeow/` that holds a `*whatsmeow.Client`, an `*sqlstore.Container`, the JID translator, the event channel, and the `clientCtx`. Implements all seven port interfaces from `internal/app/ports.go`.
- **JID Translator**: A pair of pure functions `toDomain(types.JID) (domain.JID, error)` and `toWhatsmeow(domain.JID) types.JID` lifted into the adapter package. Translates losslessly via the canonical `<user>@<server>` string form.
- **Event Translator**: A pure function `translateEvent(any) (domain.Event, error)` that switches on the whatsmeow event type and produces a domain variant, assigning a monotonically-increasing `domain.EventID`.
- **Session Container**: A wrapper around `whatsmeow/sqlstore.Container` living under `internal/adapters/secondary/sqlitestore/`. Holds the SQLite handle, the file lock, and the device store. Constructed once at adapter startup.
- **Pairing Harness**: An integration-test binary or test helper under `internal/adapters/secondary/whatsmeow/internal/pairtest/` that exercises QR and phone-code flows against a real WhatsApp account, gated by `WA_INTEGRATION=1`. NOT a CLI, NOT under `cmd/`.

## Success Criteria *(mandatory)*

### Measurable Outcomes

- **SC-001**: After feature 003 lands, a fresh clone with a paired burner number can run `go test -race -tags integration -run Contract ./internal/adapters/secondary/whatsmeow/...` and observe **all** contract test cases from `internal/app/porttest/` pass against the whatsmeow adapter — exactly the same set that pass against the in-memory adapter from feature 002. Verifiable by counting test names and asserting they match.
- **SC-002**: `git diff main..003-whatsmeow-adapter -- internal/domain internal/app/ports.go` returns zero output. Feature 003 introduces zero changes to the hexagonal core. Verifiable from any clone.
- **SC-003**: Running `go list -deps ./internal/domain/...` and `go list -deps ./internal/app/...` returns zero results containing `go.mau.fi/whatsmeow`. The depguard rule remains active and effective. Verifiable from any clone.
- **SC-004**: A second adapter constructor call against the same SQLite file path within the same OS user fails with a typed "session locked" error within 100ms, without touching the database file. Verifiable by a unit test that uses two `*sqlstore.Container` constructions in sequence.
- **SC-005**: Pairing a fresh number from QR-in-terminal completes in under 60 seconds end-to-end on a normal residential connection (the user has 60 seconds to scan; the adapter takes <2s after the scan). Verifiable manually with a stopwatch.
- **SC-006**: Pairing a fresh number from phone-pairing-code completes in under 90 seconds end-to-end. Verifiable manually with a stopwatch.
- **SC-007**: After a paired session is established, restarting the adapter process and reconnecting to the websocket completes in under 5 seconds with no user interaction. Verifiable by stopwatch.
- **SC-008**: The total LOC introduced by this feature under `internal/adapters/secondary/whatsmeow/` and `internal/adapters/secondary/sqlitestore/` is **under 1500 lines** including doc comments, excluding tests. The goal is a thin translation layer, not a re-implementation. Verifiable by `find ... -name '*.go' | grep -v _test.go | xargs wc -l`.
- **SC-009**: A whatsmeow Renovate bump opens a PR whose body contains the upstream commit range (per `fetchChangeLogs: branch`) and whose CI run takes under 5 minutes to report pass/fail on the contract suite. Verifiable from the next bump after this feature merges.

## Assumptions

- The constitution at `.specify/memory/constitution.md` v1.0.0 binds; principles I (hexagonal core), III (safety — partial because rate limiter is feature 004), IV (no CGO), V (spec-driven), VI (port-boundary fakes), and VII (conventional commits) all apply. The `depguard` rule `core-no-whatsmeow` is the mechanical enforcement of Principle I.
- The seven port interfaces and their behavioural contracts from feature 002 are immutable for this feature. Any port change requires a separate `/speckit:specify` cycle on a new branch.
- The user has access to a paired WhatsApp burner number for integration tests, OR the integration tests stay on a developer's local machine and CI runs only the unit tests. Either is acceptable; the spec does not require CI to run integration tests.
- The whatsmeow upstream commit pinned in `go.mod` at the start of this feature is the one against which the contract suite is verified. Renovate bumps after this feature merges are tracked separately (US4).
- `modernc.org/sqlite` (CGO-free) is the SQLite driver used by the `sqlstore` package, matching CLAUDE.md §"Locked decisions". CGO is forbidden by Principle IV.
- The pairing harness is a minimal integration helper used by maintainers for one-off pairing; it is NOT a CLI binary, NOT a daemon, and does NOT belong under `cmd/`. It lives under `internal/adapters/secondary/whatsmeow/internal/pairtest/` and is exercised manually.
- Audit log writing is the daemon's job (feature 004). The whatsmeow adapter implements `AuditLog` as an in-memory ring buffer for tests; the file-backed implementation lands in `internal/adapters/secondary/slogaudit/` later.
- `mautrix/whatsapp` and `aldinokemal/go-whatsapp-web-multidevice` source code (cloned during feature 001 research) remain the canonical references for whatsmeow client setup and pairing-context lifetime. The `pkg/connector/client.go` 12-flag setup is reproduced verbatim per FR-009.
- This feature ships **zero** distribution artefacts and **zero** code under `cmd/`. The `.gitkeep` placeholders under `cmd/wa/`, `cmd/wad/`, `internal/adapters/primary/socket/`, `internal/adapters/secondary/slogaudit/` survive untouched. Only `internal/adapters/secondary/whatsmeow/.gitkeep` and `internal/adapters/secondary/sqlitestore/.gitkeep` are deleted by this feature.
