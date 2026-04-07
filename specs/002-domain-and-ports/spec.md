# Feature Specification: Domain Types and the Seven Port Interfaces

**Feature Branch**: `002-domain-and-ports`
**Created**: 2026-04-06
**Status**: Draft
**Input**: User description: "002 — domain types and the seven port interfaces"

## User Scenarios & Testing *(mandatory)*

### User Story 1 - A future feature can model the WhatsApp domain in pure Go (Priority: P1)

A maintainer working on feature 003 (the whatsmeow secondary adapter) or feature 004 (the daemon composition root) needs to translate raw WhatsApp protocol values into a small set of pure-Go domain types — `JID`, `Contact`, `Group`, `Message`, `Session`, `Event`, `Allowlist`, `Action` — and to trust that those types enforce every business invariant before the data ever reaches infrastructure code. They must be able to import the domain package, instantiate any type, call its validation method, and either get a clear error or a usable value, without ever loading a WhatsApp library, a database driver, or a logger.

**Why this priority**: Every line of code in features 003–007 either consumes or produces a domain type. If the domain layer is missing, malformed, or leaks infrastructure types, the whole architecture collapses into a thin wrapper around `*whatsmeow.Client` and the hexagonal invariant from the constitution becomes theatre. Nothing else can start until this exists.

**Independent Test**: Open a Go REPL or write a 10-line test program that imports `internal/domain`, constructs a `JID` from a phone string, builds a `Message`, calls `Validate()`, and prints the result — all without importing a single non-stdlib package besides the project's own domain. The exercise must work with a clean `go.sum` containing zero adapter dependencies.

**Acceptance Scenarios**:

1. **Given** a phone number `"+5511999999999"`, **When** a caller asks the domain to construct a personal JID, **Then** the domain returns a `JID` value whose string form is `"5511999999999@s.whatsapp.net"` and whose `IsGroup()` method returns false.
2. **Given** a malformed input like `"not-a-jid"`, **When** the domain attempts to parse it, **Then** the domain returns a typed error and never panics.
3. **Given** a text message body 80 KB long, **When** the caller calls `Message.Validate()`, **Then** the domain rejects it as exceeding the documented 64 KB text limit, with a clear error message naming the limit.
4. **Given** an `Allowlist` configured with `read` for one JID and `send` for another, **When** the caller asks `Allows(jid, Action)` for every combination, **Then** every answer matches the configured policy and the call has no side effects.

---

### User Story 2 - A future feature can plug into the application core through stable port interfaces (Priority: P1)

A maintainer working on the future daemon (`wad`) needs to wire any number of secondary adapters (whatsmeow, in-memory fakes, an eventual Cloud-API adapter) and any number of primary adapters (the unix-socket JSON-RPC server, a future REST server, a future MCP shim) against the **same** application use cases. They must be able to declare a use case once, depend only on a small set of port interfaces, and let the composition root inject whichever adapter is appropriate for that build.

**Why this priority**: Hexagonal architecture is only worth its tax if more than one primary adapter or more than one secondary adapter ever exists. The constitution and CLAUDE.md commit to five primary adapters (cli, socket, REST, MCP, channel) and at least one secondary swap. If the seven ports are not declared up front, every later feature will discover the need to refactor existing use cases — exactly the cost the architecture was meant to prevent.

**Independent Test**: A maintainer writes a minimal use case that takes any subset of the seven ports as constructor arguments and exercises every port at least once. They run that use case against the in-memory adapter from User Story 3 and confirm it compiles, runs, and produces the expected output, all without touching `internal/domain` or any adapter's internals.

**Acceptance Scenarios**:

1. **Given** the seven port interfaces, **When** a developer reads their signatures, **Then** they can identify which port to use for sending a message, fetching a contact, listing groups, persisting a session, deciding allow/deny, recording an audit entry, and consuming inbound events — without ambiguity or overlap.
2. **Given** an attempt to import `go.mau.fi/whatsmeow` from any file under `internal/domain` or `internal/app`, **When** the linter runs, **Then** the linter blocks the build with a clear error naming the violated rule.
3. **Given** the inbound `EventStream` port, **When** a use case calls `Next()`, **Then** it receives one event at a time (pull-based) rather than receiving a channel or a callback (push-based).

---

### User Story 3 - Every use case is unit-testable without WhatsApp (Priority: P2)

A maintainer wants to write a use case test that exercises real business logic — allowlist checks, rate-limiter delegation, message validation, audit logging — and have the test run in milliseconds, deterministically, with zero network access and zero filesystem dependencies beyond the test binary itself.

**Why this priority**: Without an in-memory implementation of every port, every test either reaches the real WhatsApp servers (impossible in CI without a burner number) or invents bespoke per-test mocks (which couple tests to call order and rot fast). The in-memory adapter is the load-bearing part of the test strategy declared in CLAUDE.md and Constitution Principle VI.

**Independent Test**: Run `go test ./internal/app/...` on a fresh clone with no environment variables set and no network. Every test passes in under one second. The same test file, with one line changed (the adapter constructor), will run against the real whatsmeow adapter in feature 003+ when `WA_INTEGRATION=1` is set.

**Acceptance Scenarios**:

1. **Given** an in-memory adapter constructed with no arguments, **When** a use case calls every port method, **Then** the calls return deterministic values, never time out, and never make a network call.
2. **Given** a test that asks the in-memory adapter to record three sent messages, **When** the test then queries the adapter's recorded calls, **Then** the recorded list contains exactly those three messages in order.
3. **Given** a parallel test run (`go test -race -p 8`), **When** multiple in-memory adapter instances run in parallel, **Then** there are no data races and no shared state across instances.

---

### User Story 4 - Any new adapter can be validated against a single contract test suite (Priority: P2)

A maintainer adding a future adapter — the whatsmeow secondary adapter in feature 003, or hypothetically a Cloud API adapter much later — needs a single shared test suite they can point at their new implementation to verify it satisfies every port contract identically. If their adapter passes the suite, they have machine-checked evidence that it is interchangeable with every other adapter without rewriting use case tests.

**Why this priority**: This is the second half of the testing strategy. Without a shared contract suite, every adapter will be tested in isolation, every adapter will diverge subtly from the others, and the "swap whatsmeow for Cloud API" claim in the architecture becomes aspirational rather than mechanical.

**Independent Test**: A maintainer calls the contract suite from the in-memory adapter's test file in this feature and from the whatsmeow adapter's test file in feature 003. Both invocations produce identical reports — same number of cases, same expected behaviors. If the whatsmeow adapter ever returns a different result than the in-memory one, the suite fails with a precise diff naming the divergence.

**Acceptance Scenarios**:

1. **Given** the contract test suite, **When** any adapter implementing the seven ports calls it with itself as the subject, **Then** every test case runs against the real implementation and reports pass/fail individually.
2. **Given** two adapters that both pass the suite, **When** they are swapped in the same use case, **Then** the use case behaves identically — same return values, same error categories, same observable side effects.
3. **Given** an adapter that violates a port contract (e.g., returns `nil` where a typed error is required), **When** it runs the suite, **Then** the suite reports the violation by name with enough context to locate the offending method.

---

### Edge Cases

- **What happens when a JID is constructed from a phone number with a `+` prefix and spaces?** The domain normalises the input by stripping non-digits before validating; trailing newlines and spaces never produce a different JID than the cleaned digits.
- **What happens when a `Message` body is empty?** The domain treats empty text as invalid; reactions and media messages may have an empty caption but the discriminating field of their variant must still be set.
- **What happens when the same JID is added to the allowlist twice with different action sets?** The second add unions with the first; the allowlist never silently downgrades a previously granted permission.
- **What happens when the contract test suite encounters an adapter that omits one of the seven port methods?** The Go compiler refuses to build the test binary, surfacing the missing-method error before any test runs.
- **What happens when the in-memory adapter is asked to deliver an event before any has been recorded?** It blocks the calling test until an event is enqueued or the test's context deadline cancels the wait — no spinning, no busy loops, no panic.
- **What happens when feature 003 needs to add an eighth port?** The constitution does NOT mandate a fixed port count — Cockburn's original 2005 paper explicitly says *"the number six is not important... it is a symbol for the drawing, and leaves room to insert ports and adapters as needed"* (cited in [`docs/reliability.md`](../../docs/reliability.md) §D2). Adding a new port requires three things, all in the same PR: (1) add the interface to `internal/app/ports.go` with full signatures and doc comments, (2) extend the contract test suite under `internal/app/porttest/` with at least one positive and one negative case per method, (3) update CLAUDE.md §"Reliability principles" rule 21 if the completeness test (every port used, every use case expressible) is affected. Removing a port requires the same three steps in reverse plus a check that no future feature still depends on it.

## Requirements *(mandatory)*

### Functional Requirements

- **FR-001**: The repository MUST contain a `domain` package whose types include at minimum `JID`, `Contact`, `Group`, `Message`, `Session`, `Event`, `Allowlist`, `Action`, `MessageID`, `EventID`, and `AuditEvent`.
- **FR-002**: Every domain type MUST have at least one method beyond field accessors. Anemic types are forbidden; the constitution's anti-pattern #2 is enforced at code-review time.
- **FR-003**: The `JID` type MUST parse and validate `<digits>@s.whatsapp.net` and `<id>@g.us`, MUST reject any other shape with a typed error, and MUST never panic on malformed input.
- **FR-004**: The `Message` type MUST enforce a 64 KB cap on text bodies and a 16 MB cap on media payloads, returning a clear error that names the offending limit when exceeded.
- **FR-005**: The `Allowlist` type MUST expose an `Allows(jid, action)` decision returning a boolean, MUST treat unknown JIDs as denied by default, and MUST be safe for concurrent reads.
- **FR-006**: The `Action` type MUST enumerate at minimum `read`, `send`, `group.add`, and `group.create` and MUST be exhaustively switchable in callers (the compiler should warn on missing branches when a new action is added).
- **FR-007**: The repository MUST contain an `app` package with a `ports.go` file declaring at least the seven exported interface types currently named in `CLAUDE.md` §"Ports": `MessageSender`, `EventStream`, `ContactDirectory`, `GroupManager`, `SessionStore`, `Allowlist`, and `AuditLog`. The names and shapes MUST match the locked decisions there. The set is the *current* port set, not a fixed count — future features may add or split ports following the procedure in `spec.md` Edge Cases and the rationale in [`docs/reliability.md`](../../docs/reliability.md) §D2.
- **FR-008**: `EventStream` MUST be pull-based: it MUST expose a method that returns the next event under a caller-supplied cancellation context, not a Go channel field and not a callback registration.
- **FR-009**: No file under `internal/domain/**` or `internal/app/**` MAY import `go.mau.fi/whatsmeow` or any of its subpackages. The `golangci-lint` rule `core-no-whatsmeow` (already configured in `.golangci.yml`) MUST fail the build on violation.
- **FR-010**: The repository MUST contain an in-memory implementation of all seven ports under `internal/adapters/secondary/memory/`. The implementation MUST be deterministic, MUST require zero environment configuration, and MUST run without network or filesystem access beyond the test binary.
- **FR-011**: The repository MUST contain a contract test suite under `internal/app/porttest/` that any adapter implementing the seven ports can run against itself. The suite MUST exercise at least one positive and one negative case per port method.
- **FR-012**: The in-memory adapter MUST pass the contract test suite when invoked with `go test ./internal/app/porttest/... -run In_Memory`.
- **FR-013**: `go test ./...` MUST exit 0 in CI on this branch with no network access and no environment variables beyond the runner defaults.
- **FR-014**: `golangci-lint run ./...` MUST exit 0 with the existing `.golangci.yml` config (including the `core-no-whatsmeow` rule, `forbidigo`, `gocyclo`, `gosec`, and the rest).
- **FR-015**: Every exported function and type in `internal/domain` MUST have a doc comment that names what the value represents and what callers may assume about it.
- **FR-016**: The feature MUST NOT introduce a daemon, a CLI client, a unix socket, a SQLite database, a real WhatsApp connection, or any other infrastructure concern. Those belong to features 003 (whatsmeow adapter) and 004 (daemon and socket) respectively.
- **FR-017**: The feature MUST NOT delete or modify any file under `cmd/` or any file outside `internal/domain/`, `internal/app/`, `internal/adapters/secondary/memory/`, the new `internal/app/porttest/`, and the spec/checklist artefacts. The other `.gitkeep` placeholders survive untouched.

### Key Entities

- **Domain Type**: A pure-Go value or struct living under `internal/domain` whose construction validates business invariants and whose methods express domain operations. Has zero non-stdlib imports and zero awareness of WhatsApp infrastructure.
- **Port Interface**: A small Go interface declared in `internal/app/ports.go` that names a single capability the application core needs from the outside world. Every port has at least one production implementation and at least one in-memory implementation.
- **In-Memory Adapter**: A struct under `internal/adapters/secondary/memory` that implements one or more port interfaces using only stdlib data structures, with no goroutines, no network I/O, and no time dependencies beyond an injectable clock.
- **Contract Test Case**: A Go test function inside `internal/app/porttest/` that takes an adapter as input and asserts a single behavioural rule. The suite of all such tests is the canonical "what does this port mean" definition; passing the suite is the necessary condition for an adapter to be considered interchangeable.
- **Composition Root** (out of scope but referenced): The future code in `cmd/wad/main.go` (feature 004) that will instantiate the concrete adapters and inject them into use cases. It must be able to swap the in-memory adapter for the whatsmeow adapter without changing any use case file.

## Success Criteria *(mandatory)*

### Measurable Outcomes

- **SC-001**: A maintainer can read the seven port interfaces in under 10 minutes and correctly explain which port to use for any of the eleven JSON-RPC methods listed in `CLAUDE.md` §"Daemon, IPC, single-instance" — verifiable by walking the table and naming the port.
- **SC-002**: `go test ./...` on a fresh clone with no network and no environment variables passes in under 5 seconds and exercises at least one test per port method via the contract suite.
- **SC-003**: `golangci-lint run ./...` reports zero findings on this branch, including the `core-no-whatsmeow` `depguard` rule.
- **SC-004**: Introducing a deliberate violation — a single line `import "go.mau.fi/whatsmeow"` in any file under `internal/domain` or `internal/app` — causes `golangci-lint run` to fail within 30 seconds with an error message naming the rule and the file.
- **SC-005**: Feature 003 (the whatsmeow secondary adapter) can begin implementation with zero modifications to any file under `internal/domain` or `internal/app/ports.go`. Verifiable by running `git diff main..002-domain-and-ports -- internal/domain internal/app/ports.go` after feature 003 is feature-complete and confirming the diff is empty.
- **SC-006**: A new contributor can write a use case test for any combination of the seven ports in under 10 lines of test setup using the in-memory adapter — verifiable by writing one such test as part of this feature's quickstart.
- **SC-007**: The contract test suite covers 100 % of the methods declared on the seven port interfaces. Verifiable by counting interface methods and counting test functions in `porttest/`.
- **SC-008**: Every exported domain type has a doc comment, and `go vet ./...` plus `golangci-lint run` together produce zero comment-related findings.

## Assumptions

- The constitution at `.specify/memory/constitution.md` v1.0.0 is binding for this feature. Principles I (hexagonal core), II (daemon owns state), V (spec-driven with citations), VI (port-boundary fakes), and VII (conventional commits) all apply directly. Principles III (safety) and IV (no CGO) apply transitively — the domain has no rate limiter (that lives in feature 004's middleware), but the `Allowlist` and `Action` types provide the data structures the rate limiter and policy middleware will consume.
- The seven port names and their high-level shapes are already locked in [`CLAUDE.md`](../../CLAUDE.md) §"Ports". This feature finalises the exact Go signatures (parameter names, error types, helper structs) but does not relitigate the seven names.
- No real WhatsApp connection, no SQLite store, no socket, no goroutines outside test helpers. Anything that needs the network belongs in feature 003+.
- The in-memory adapter is the seed for a future `--dry-run` mode in feature 005's CLI; this feature builds it for tests but designs it so a dry-run mode can later reuse it without modification.
- "Tests" in spec.md user stories refer to Go test files; the constitution mandates the port-boundary fake pattern, so the tests are not optional even though the spec template treats them as such.
- Go toolchain ≥ 1.22, `golangci-lint` v2.x latest (per the `.github/workflows/ci.yml` pin), `gofumpt`, `lefthook` available locally — all already provisioned by feature 001's governance pass.
- The future whatsmeow adapter (feature 003) will validate this spec by passing the contract test suite. Any port method that turns out to be impossible to implement against whatsmeow forces an amendment to this spec, the constitution's "exactly seven ports" rule, and the contract suite simultaneously.
- This feature ships **zero** distribution artefacts (no GoReleaser, no Nix flake build, no Homebrew tap update). Distribution lands in feature 006.
