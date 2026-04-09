# Implementation Plan: Socket Primary Adapter

**Branch**: `004-socket-adapter` | **Date**: 2026-04-08 | **Spec**: [spec.md](spec.md)
**Input**: Feature specification from `/specs/004-socket-adapter/spec.md`

## Summary

Deliver the transport layer of `wad`: a line-delimited JSON-RPC 2.0 server over a per-user unix domain socket. The implementation uses `github.com/creachadair/jrpc2` with its built-in `channel.Line` framing and `AllowPush` server-initiated notifications so the `subscribe` method can stream WhatsApp events to the thin `wa` client. A `Dispatcher` interface is the sole seam between this adapter and the to-be-written use cases in feature 005; every contract test exercises the server against an in-package fake dispatcher, so this feature ships in isolation. Same-user-only authentication is enforced at accept time via `SO_PEERCRED` on Linux and `LOCAL_PEERCRED` / `Getpeereid` on macOS through `golang.org/x/sys/unix`, with no CGO. Single-instance enforcement reuses feature 003's `rogpeppe/go-internal/lockedfile` — specifically `lockedfile.Mutex` on a `.lock` sibling file. Graceful shutdown follows Eli Bendersky's canonical `context + WaitGroup + listener.Close` pattern; deadline tests are made deterministic with `testing/synctest` (Go 1.25+), and goroutine leaks are caught by `go.uber.org/goleak` in `TestMain`.

## Technical Context

**Language/Version**: Go 1.25 (toolchain pinned in `go.mod`; `testing/synctest` is GA since 1.25)
**Primary Dependencies**:
- `github.com/creachadair/jrpc2` — JSON-RPC 2.0 framing, dispatch, server push
- `golang.org/x/sys/unix` — peer credential syscalls (`GetsockoptUcred`, `GetsockoptXucred`, `Getpeereid`)
- `github.com/rogpeppe/go-internal/lockedfile` — single-instance file lock (already in feature 003)
- `github.com/adrg/xdg` — XDG path resolution on Linux only; darwin path is computed manually (see §Contradicts blueprint in research.md)
- `log/slog` (stdlib) — structured logging
- `go.uber.org/goleak` (test only) — goroutine leak detection

**Storage**: None. The socket path lives on the filesystem but holds no data; the `.lock` sibling file is zero-byte by design.

**Testing**:
- Unit + contract tests in `internal/adapters/primary/socket/` using the in-package fake dispatcher
- Contract suite in `internal/adapters/primary/socket/sockettest/` runnable against any `Dispatcher` implementation
- `testing/synctest` bubbles for deadline-sensitive tests (graceful shutdown, backpressure)
- `go.uber.org/goleak` in `TestMain` for the whole package; baseline no leaked goroutines
- OS-specific files use build tags `//go:build linux` / `//go:build darwin` for the peer-cred implementation

**Target Platform**: Linux and macOS. Same unix-domain-socket API on both; only the peer-cred syscall differs. Windows is explicitly unsupported in v0.1 (CLAUDE.md).

**Project Type**: Library package inside the `wa` monorepo. Not a standalone binary. Feature 006 will construct a `Server` instance from `cmd/wad/main.go` and wire it to the real use cases.

**Performance Goals**:
- Request/response roundtrip against fake dispatcher: ≤10 ms on a developer laptop (spec SC-001)
- Sustain 1000 sequential request/response cycles with <10 MiB RSS growth (spec SC-004)
- Accept ≥16 concurrent connections without error (spec FR-027)
- Pipeline up to 32 in-flight requests per connection (spec FR-028)

**Constraints**:
- `CGO_ENABLED=0` everywhere (constitution principle IV)
- No import of `go.mau.fi/whatsmeow` from this package or any other core package (constitution principle I, enforced by depguard)
- Single framed message ≤ 1 MiB; oversize → parse error + connection close
- Peer-cred check must complete within 50 ms on normal paths; 1-second hard cap before forced drop
- Graceful shutdown deadline: 5 seconds default drain + 1-second margin
- No contents of `params` or `result` may appear in any log line (spec FR-038)

**Scale/Scope**:
- ~1500 LOC expected: transport ~600, dispatcher + error table ~200, contracts ~200, tests ~500
- ~15 Go files in the new package plus 5 in the `sockettest/` contract suite
- 10-step quickstart reproducible in under 5 minutes on a fresh clone

## Constitution Check

*GATE: Must pass before Phase 0 research. Re-check after Phase 1 design.*

All seven core principles from [`.specify/memory/constitution.md`](../../.specify/memory/constitution.md) evaluated against the Phase 0/1 plan below.

| # | Principle | Status | Notes |
|---|---|---|---|
| I | Hexagonal core, library at arm's length | ✅ PASS | This feature lives under `internal/adapters/primary/socket/`. It imports `internal/app/ports.go` only indirectly through the `Dispatcher` interface it defines here; it does NOT import any `whatsmeow` subpackage. The existing `.golangci.yml` `core-no-whatsmeow` rule continues to apply. A new depguard rule will be added to forbid `go.mau.fi/whatsmeow` imports from `internal/adapters/primary/**` as well, hardening the boundary. |
| II | Daemon owns state, CLI is dumb | ✅ PASS | The socket adapter is the server side of the daemon/client split. It holds no business state; the `Dispatcher` interface is stateless from the adapter's perspective. `cmd/wa` will consume this via JSON-RPC, holding no session state. |
| III | Safety first, no `--force` ever | ✅ PASS | No dispatcher methods are defined in this feature, so no business operations can be exposed yet. The rate limiter and allowlist will land in feature 005 and be invoked by the use cases that implement the `Dispatcher` interface — the socket adapter is a pass-through and has no bypass mechanism. |
| IV | CGO is forbidden | ✅ PASS | Every dependency (`jrpc2`, `x/sys/unix`, `lockedfile`, `adrg/xdg`, `slog`, `goleak`) is pure Go. Peer-credential syscalls are invoked via `x/sys/unix` wrappers, not CGO. Contract tests use `net.Pipe` or `socketpair(2)` via `unix.Socketpair`, both CGO-free. |
| V | Spec-driven with citations | ✅ PASS | Every technology choice in research.md has a primary-source URL. One explicit `## Contradicts blueprint` section records the CLAUDE.md darwin path mismatch per the principle's requirement. |
| VI | Tests use port-boundary fakes | ✅ PASS | Every test in this feature uses an in-package fake dispatcher, not real whatsmeow. No test in this feature imports `go.mau.fi/whatsmeow/...`. No `//go:build integration` gate is required; all tests are deterministic and run in CI. |
| VII | Conventional commits, signed tags, no `--no-verify` | ✅ PASS | Feature branch `004-socket-adapter` will land via PR with Conventional Commit messages per the lefthook `commit-msg` hook introduced in PR #1. No amendments to the governance flow. |

**Gate result**: PASS. Zero violations. Phase 0 may proceed.

## Project Structure

### Documentation (this feature)

```text
specs/004-socket-adapter/
├── plan.md                      # This file
├── research.md                  # Phase 0 — D1..D13 decision blocks with citations
├── data-model.md                # Phase 1 — entities, state machines, invariants
├── quickstart.md                # Phase 1 — 10-step reproducible bring-up
├── contracts/
│   ├── wire-protocol.md         # JSON-RPC framing, error code table, reserved methods
│   ├── dispatcher.md            # Dispatcher interface contract
│   └── socket-path.md           # Platform-specific socket path resolver contract
├── checklists/
│   └── requirements.md          # Spec quality checklist (already written)
└── tasks.md                     # Phase 2 — /speckit:tasks output, NOT created here
```

### Source Code (repository root)

New code lives entirely under two sibling directories, mirroring the existing primary-adapter slot that's been empty since feature 001:

```text
internal/adapters/primary/socket/
├── doc.go                       # package doc: transport layer, no business logic
├── server.go                    # Server struct, Run, Shutdown, Wait
├── listener.go                  # net.Listen("unix", …), parent-dir create, mode 0600
├── path_linux.go                # //go:build linux — $XDG_RUNTIME_DIR/wa/wa.sock
├── path_darwin.go               # //go:build darwin — ~/Library/Caches/wa/wa.sock
├── peercred_linux.go            # //go:build linux — GetsockoptUcred
├── peercred_darwin.go           # //go:build darwin — Getpeereid
├── accept.go                    # accept loop, peer-cred gate, connection bookkeeping
├── connection.go                # per-conn goroutine, bounded outbound mailbox
├── dispatch.go                  # jrpc2 wiring; Dispatcher interface call-out
├── dispatcher.go                # the Dispatcher interface definition (the seam)
├── errors.go                    # ErrAlreadyRunning, ErrBackpressure, ErrShutdown, etc.
├── errcodes.go                  # JSON-RPC error code table (domain → code mapping)
├── subscribe.go                 # subscribe/unsubscribe methods + per-conn filter table
├── lifecycle.go                 # context/WaitGroup/shutdown deadline
└── lock.go                      # lockedfile.Mutex single-instance gate

internal/adapters/primary/socket/sockettest/
├── doc.go                       # package doc: reusable contract suite
├── suite.go                     # entry point: RunSuite(t, newServer)
├── fake_dispatcher.go           # FakeDispatcher implementing Dispatcher
├── helpers.go                   # socketpair, connect, send/recv line-delimited JSON
├── request_response_test.go     # US1 contract tests
├── peercred_test.go             # US2 contract tests (with build-tag guards)
├── subscribe_test.go            # US3 contract tests (uses synctest for backpressure)
├── single_instance_test.go      # US4 contract tests
├── shutdown_test.go             # US5 contract tests (uses synctest for deadlines)
└── leak_test.go                 # TestMain wiring goleak.VerifyTestMain

internal/adapters/primary/socket/
└── server_test.go               # in-package tests exercising private surfaces
```

**Structure Decision**: The feature fits the existing hexagonal layout with no new top-level directories. `internal/adapters/primary/socket/` is the slot the repository already left empty in feature 001. The contract suite sits in a sibling `sockettest/` package so that feature 005's use-case tests — and future REST/MCP adapters — can reuse it by passing their own `Dispatcher` implementation. The OS-specific files use Go build tags rather than a runtime `switch` so the Linux and darwin code paths are verified independently by `go build ./...` and by CI running on both GOOS values.

## Complexity Tracking

> No constitution violations. This section is intentionally empty.

The three scope decisions that could look like complexity but are justified by explicit spec requirements:

| Apparent complexity | Why it's necessary | Simpler alternative rejected because |
|---|---|---|
| OS-specific peer-cred files (build-tagged) | FR-013 requires peer uid on both Linux and macOS; `x/sys/unix` has different syscall names on each OS | Runtime `runtime.GOOS` switch is equally valid but splits compile-time checking; build tags make the matrix explicit |
| Separate `sockettest/` contract suite package | Feature 005 and future primary adapters will reuse the same contract suite against their own Dispatcher implementations | In-package tests would trap the contracts inside a private test package, forcing duplication |
| Sibling `.lock` file instead of flock on the socket itself | `lockedfile.Mutex` works on a regular file; the unix socket inode is not a regular file and cannot be `flock()`'d reliably across platforms | `flock(socketfd)` on Linux works but is undefined on darwin; a `.lock` sibling is portable and matches feature 003's pattern |
