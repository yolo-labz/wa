# Implementation Plan: whatsmeow Secondary Adapter

**Branch**: `003-whatsmeow-adapter` | **Date**: 2026-04-07 | **Spec**: [`spec.md`](./spec.md)
**Input**: Feature specification from `/specs/003-whatsmeow-adapter/spec.md`

## Summary

Wrap `go.mau.fi/whatsmeow` in a secondary adapter at `internal/adapters/secondary/whatsmeow/` that satisfies all eight port interfaces from feature 002 (the original seven plus the new `HistoryStore` resolved by `/speckit:clarify`). Add two SQLite-backed sibling adapters: `sqlitestore/` for whatsmeow's Signal ratchet store, and `sqlitehistory/` for our message history with FTS5. Pass the contract test suite from `internal/app/porttest/` against the new adapter to prove behavioural equivalence with the in-memory fake. Bound history sync at the source via `DeviceProps.HistorySyncConfig{7 days, 20 MB, 100 MB}` per the mautrix-whatsapp production pattern; expose on-demand expansion via `HistoryStore.LoadMore`. Daemon, CLI, JSON-RPC socket, rate limiter, and audit log file writer remain out of scope (features 004–005).

## Technical Context

**Language/Version**: Go 1.22 minimum (constitution); dev host pinned in `go.mod` at `go 1.26.1`.
**Primary Dependencies**: `go.mau.fi/whatsmeow` (commit-pinned via `go.sum` pseudo-version, Renovate-managed per `renovate.json`), `modernc.org/sqlite` (CGO-free, FTS5 enabled via build tag `sqlite_fts5`), `mdp/qrterminal/v3` (QR rendering), `adrg/xdg` (already in go.mod from feature 002 — no new dep). Standard library `flock` via `golang.org/x/sys/unix` (or stdlib `syscall.Flock` on linux/darwin).
**Storage**: TWO independent SQLite files at `$XDG_DATA_HOME/wa/`: `session.db` (whatsmeow ratchets, owned by `sqlitestore`) and `messages.db` (our message history with FTS5, owned by `sqlitehistory`). Each is `chmod 0600` and individually `flock`'d.
**Testing**: `go test -race ./...` for unit tests of JID translator, event translator, file-permission setter, flock guard, FTS5 schema. Contract suite from `internal/app/porttest/` runs against the new adapter under `//go:build integration` and `WA_INTEGRATION=1`. The new `HistoryStore` port gets a contract test in the same suite (extending `porttest/historystore.go`).
**Target Platform**: macOS arm64 + Linux (amd64/arm64). Windows is intentionally excluded — the daemon supervisor model (launchd/systemd user units) is unix-only.
**Project Type**: Go monorepo. Two NEW adapter packages this feature: `whatsmeow/` and `sqlitehistory/`. One MODIFIED package: `sqlitestore/` (was `.gitkeep` only, now real). Two minimally-modified files: `internal/domain/errors.go` (one new sentinel `ErrDisconnected`) and `internal/app/ports.go` (one new interface `HistoryStore`).
**Performance Goals**: First pair completes in <60s end-to-end (SC-005). Reconnect after restart <5s (SC-007). Initial history sync downloads <20 MB (FR-019). On-demand `HistoryStore.LoadMore` for a single chat returns local results in <50ms; a remote round-trip via `BuildHistorySyncRequest` returns within 30s timeout. The contract suite runs in <10s on the in-memory adapter and <60s on the whatsmeow adapter (with a real burner number).
**Constraints**: Zero CGO. Zero infrastructure types in domain or app. Zero non-stdlib imports under `internal/domain/`. Zero `whatsmeow/types.JID` values escape `internal/adapters/secondary/whatsmeow/`. The `clientCtx` is daemon-scoped, NOT request-scoped (per FR-012 and the aldinokemal lesson). Single-instance enforcement via two `flock`s.
**Scale/Scope**: ~2200 LOC across the three adapter packages (FR-008 budget). ~30 contract test functions (8 ports × ~3-6 cases each). One maintainer, one workstation.

## Constitution Check

*GATE: Must pass before Phase 0 research. Re-check after Phase 1 design.*

| # | Principle | Verdict | Evidence |
|---|---|---|---|
| I | Hexagonal core (NON-NEGOTIABLE) | **PASS — with documented amendment** | The `core-no-whatsmeow` `depguard` rule continues to forbid whatsmeow imports under `internal/domain/` and `internal/app/`. The two minimal modifications outside the adapter packages — `domain.ErrDisconnected` (one var) and `HistoryStore` interface (one signature) — both remain stdlib-only and contain ZERO whatsmeow types. The depguard rule still passes. The amendment is documented in spec.md FR-021/FR-022 and `## Clarifications` session 2026-04-07 per CLAUDE.md rule 20 (no fixed port count). |
| II | Daemon owns state | **N/A** | This feature has no daemon code; the adapter is a library that the future feature 004 daemon will compose. |
| III | Safety first, no `--force` | **PARTIAL — JUSTIFIED** | The adapter implements `AuditLog` as a small in-memory ring buffer for tests (FR-016 carve-out). The file-backed audit log writer + rate limiter middleware + warmup ramp + allowlist file watcher all land in feature 004 alongside the daemon clock. The `<channel source="wa">` tag wrapper is feature 005's CLI / future plugin's concern. This split is the same one feature 002 carried; nothing new. |
| IV | CGO forbidden | **PASS** | `modernc.org/sqlite` (CGO-free) for both DBs. FTS5 enabled via build tag `sqlite_fts5`, NOT CGO. `flock` via `syscall.Flock` (stdlib, no CGO). |
| V | Spec-driven with citations | **PASS** | spec.md cites the mautrix-whatsapp pattern explicitly. Clarifications session 2026-04-07 cites `BuildHistorySyncRequest`, `HistorySyncConfig`, `ManualHistorySyncDownload`, and the dossier at `docs/research-dossiers/whatsmeow-history-sync.md`. Every architectural choice traces back to a primary source. |
| VI | Tests use port-boundary fakes | **PASS** | Unit tests use the in-memory adapter from feature 002 wherever possible. The new `HistoryStore` contract tests reuse the existing factory pattern. Real WhatsApp integration is gated behind `//go:build integration` + `WA_INTEGRATION=1`. |
| VII | Conventional commits | **PASS** | Every commit landing this feature uses `feat(adapter):`, `feat(sqlitestore):`, `feat(sqlitehistory):`, `feat(ports):`, `feat(domain):`, `chore(test):`, `docs(spec):`. |

**Initial gate (Phase 0)**: PASS. Principle I is "PASS with amendment" because the spec.md amendments to FR-021/022 are documented under the procedure CLAUDE.md rule 20 explicitly permits.

**Post-design gate (Phase 1)**: PASS. The Phase 1 artefacts (data-model, contracts, quickstart) describe the same boundary the spec defines. They introduce no new architectural decisions; they refine the Go signatures.

**Complexity Tracking**: empty. The two amendments to feature 002's locked artefacts (`domain/errors.go` and `app/ports.go`) are not complexity violations — they are documented evolutions under the procedure feature 002 itself defined for this exact case.

## Project Structure

### Documentation (this feature)

```text
specs/003-whatsmeow-adapter/
├── spec.md              # /speckit:specify output + /speckit:clarify (session 2026-04-07)
├── plan.md              # this file (/speckit:plan)
├── research.md          # Phase 0: 5 tactical D blocks (this command)
├── data-model.md        # Phase 1: Go types for ErrDisconnected, HistoryStore, sqlitehistory schema
├── quickstart.md        # Phase 1: 6-step verification runbook
├── contracts/
│   ├── historystore.md      # the new 8th port: signature + behavioural contract
│   ├── whatsmeow-adapter.md # JID/event translation rules + lifecycle contract
│   └── sqlitehistory-adapter.md # FTS5 schema + concurrency contract
├── checklists/
│   └── requirements.md  # 45-item validation, 36 closed at clarify time, 9 open until /implement
└── tasks.md             # /speckit:tasks output (NOT this command)
```

### Source Code (repository root)

```text
github.com/yolo-labz/wa/
├── internal/
│   ├── domain/
│   │   ├── errors.go              # MODIFIED: + var ErrDisconnected (one line) — only domain change
│   │   └── errors_test.go         # MODIFIED: + one row in the table test
│   ├── app/
│   │   ├── ports.go               # MODIFIED: + HistoryStore interface (one block) — only app change
│   │   └── porttest/
│   │       └── historystore.go    # NEW: contract tests for the 8th port (extends the existing suite)
│   └── adapters/
│       └── secondary/
│           ├── whatsmeow/         # NEW directory (replaces .gitkeep)
│           │   ├── adapter.go         # main Adapter struct, Close(), accessors
│           │   ├── pair.go            # pairing flow (QR + phone code) per FR-008
│           │   ├── send.go            # MessageSender impl + ErrDisconnected handling
│           │   ├── stream.go          # EventStream impl over bounded channel
│           │   ├── translate_jid.go   # JID translator (whatsmeow ↔ domain)
│           │   ├── translate_event.go # Event translator (whatsmeow events ↔ domain.Event)
│           │   ├── directory.go       # ContactDirectory impl
│           │   ├── groups.go          # GroupManager impl
│           │   ├── session.go         # SessionStore impl (delegates to sqlitestore)
│           │   ├── allowlist.go       # Allowlist consumer (wraps *domain.Allowlist)
│           │   ├── audit.go           # AuditLog impl (in-memory ring buffer for v0)
│           │   ├── history.go         # HistoryStore impl + on-demand BuildHistorySyncRequest plumbing
│           │   ├── flags.go           # whatsmeow client flag constants per FR-009
│           │   ├── *_test.go          # unit tests (no whatsmeow live calls)
│           │   ├── adapter_integration_test.go # //go:build integration — runs porttest suite
│           │   └── internal/pairtest/main.go   # pairing harness (NOT a CLI; manual integration)
│           ├── sqlitestore/       # NEW directory (replaces .gitkeep)
│           │   ├── store.go           # *sqlstore.Container wrapper + flock
│           │   ├── store_test.go      # unit tests for flock contention
│           │   └── doc.go             # package doc explaining the schema is whatsmeow's, not ours
│           ├── sqlitehistory/     # NEW directory + .gitkeep replaced
│           │   ├── store.go           # HistoryStore impl
│           │   ├── schema.sql         # embedded SQL: messages table + FTS5 virtual table
│           │   ├── schema_embed.go    # //go:embed schema.sql
│           │   ├── flock.go           # second flock for messages.db
│           │   └── store_test.go      # unit tests for FTS5 search + concurrency
│           ├── memory/            # UNCHANGED (the in-memory adapter from feature 002)
│           ├── slogaudit/         # UNCHANGED (.gitkeep)
│           └── primary/           # UNCHANGED (.gitkeep)
└── cmd/                           # UNCHANGED (.gitkeep × 2)
```

The `cmd/wa/.gitkeep` and `cmd/wad/.gitkeep` placeholders survive untouched. Only the three named adapter directories are modified by this feature.

**Structure Decision**: hexagonal / ports-and-adapters per [`CLAUDE.md`](../../CLAUDE.md) §"Repository layout". Three new sibling secondary adapters because the constitution Principle I + Cockburn principle treat each adapter as owning its own persistence; merging the two SQLite stores into one package would couple whatsmeow's schema (which we do not own) to our message history (which we do).

## Phase 0 — Research

**Status**: complete. See [`research.md`](./research.md).

Five tactical decisions resolved (D1–D5):

1. **D1** Request-ID-keyed channel implementation for on-demand history sync
2. **D2** FTS5 enablement in `modernc.org/sqlite` (build tag `sqlite_fts5`)
3. **D3** `HistoryStore` lives in the same `internal/app/ports.go` file as the original 7 ports
4. **D4** Mocking strategy for the whatsmeow `*Client` in unit tests (interface extraction)
5. **D5** SQL schema bootstrap via `//go:embed schema.sql`

The architectural decisions (FR-018, FR-019, FR-020) are already resolved in `spec.md ## Clarifications` from the `/speckit:clarify` session 2026-04-07; research.md does not duplicate them. The dossier at [`docs/research-dossiers/whatsmeow-history-sync.md`](../../docs/research-dossiers/whatsmeow-history-sync.md) is the citation root.

Zero `[NEEDS CLARIFICATION]` markers remain.

## Phase 1 — Design & Contracts

**Status**: complete in this command. Four artefacts land alongside this `plan.md`:

- **[`research.md`](./research.md)** — five `Decision/Rationale/Alternatives` blocks for the tactical Go choices
- **[`data-model.md`](./data-model.md)** — Go field types and methods for the three new entities (`ErrDisconnected`, `HistoryStore` port, `sqlitehistory.Store`) plus the whatsmeow Adapter struct layout
- **[`contracts/historystore.md`](./contracts/historystore.md)** — the new 8th port's behavioural contract (HS1–HS6 clauses)
- **[`contracts/whatsmeow-adapter.md`](./contracts/whatsmeow-adapter.md)** — JID translator, event translator, lifecycle, and `clientCtx` rules
- **[`contracts/sqlitehistory-adapter.md`](./contracts/sqlitehistory-adapter.md)** — schema, FTS5 query rules, concurrency
- **[`quickstart.md`](./quickstart.md)** — verification runbook

**Agent context update** (`update-agent-context.sh`): **skipped intentionally**, same rationale as features 001 and 002. CLAUDE.md is hand-authored, lacks the marker comments the script preserves, and is the architectural source of truth.

## Complexity Tracking

> **Fill ONLY if Constitution Check has violations that must be justified**

Empty. Both amendments to feature 002's locked artefacts (`domain/errors.go` and `app/ports.go`) are documented evolutions under the procedure feature 002 itself defined for this exact case. CLAUDE.md rule 20 ("ports as intent of conversation, no fixed port count") explicitly permits adding ports. The constitution principle is upheld.

## Followup wiring for `/speckit:tasks`

When `/speckit:tasks` runs against this plan, the resulting `tasks.md` should produce a real implementation plan:

| Phase | Task range | Story |
|---|---|---|
| Setup | T001..T004 | none |
| Foundational | T005..T010 | none — `domain.ErrDisconnected`, `HistoryStore` port, `porttest/historystore.go`, in-memory adapter extension |
| US1 use-case parity | T011..T025 | `[US1]` — JID translator, event translator, send, stream, directory, groups, session, allowlist, audit, history, integration test |
| US2 pairing UX | T026..T030 | `[US2]` — pair.go (QR + phone code), pairtest harness |
| US3 single-instance store | T031..T036 | `[US3]` — sqlitestore + flock, sqlitehistory + schema + FTS5, two-flock test |
| US4 Renovate loop | T037..T038 | `[US4]` — verify renovate.json still applies, document the bump-validate cycle |
| Polish | T039..T046 | none — `go test -race`, `golangci-lint`, deliberate-violation depguard, contract suite end-to-end |

Roughly 46 tasks. ~2200 LOC. Multi-session implementation likely.
