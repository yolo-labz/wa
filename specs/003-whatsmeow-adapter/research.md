# Research: whatsmeow Secondary Adapter — Tactical Decisions

**Spec**: [`spec.md`](./spec.md) · **Branch**: `003-whatsmeow-adapter` · **Date**: 2026-04-07

The architectural decisions for feature 003 are already locked in the spec's `## Clarifications` session 2026-04-07 and the dossier at [`docs/research-dossiers/whatsmeow-history-sync.md`](../../docs/research-dossiers/whatsmeow-history-sync.md). This research file covers only the **5 remaining tactical Go-language choices** that surfaced during plan composition. Each is resolved in `Decision / Rationale / Alternatives` form with citations.

## D1 — Request-ID-keyed channel for on-demand history sync

**Decision**: A `*sync.Map` keyed by request ID (string) holding `chan *waHistorySync.HistorySync` (buffered, capacity 1). The adapter creates the channel before calling `client.BuildHistorySyncRequest`, registers it in the map under the request ID, sends the peer message, then `select`s on the channel and a `time.NewTimer(30 * time.Second)`. The event translator's `events.HistorySync` handler looks up the request ID and forwards the blob to the matching channel, then deletes the entry.

**Rationale**: This is the standard Go pattern for request/response over a callback-driven event source. `sync.Map` is correct here because (a) the access pattern is "many short-lived keys with low contention", (b) the goroutines registering and forwarding are different, and (c) we want lock-free reads from the event handler hot path. The 30-second timeout matches the upper bound on phone responsiveness observed by the mautrix project (their `backfill.go` uses 30s as well). The buffered-1 channel ensures the event-handler goroutine never blocks on a slow consumer.

**Alternatives considered**:

- **`map[string]chan` + `sync.RWMutex`**. Rejected: more verbose, equivalent semantics, and `sync.Map` is precisely engineered for this access pattern (cache-friendly amortised cost when entries are added/removed but rarely re-read).
- **A single channel + multiplex on request ID inside the consumer**. Rejected: forces every consumer to filter, defeats the point of per-request scoping.
- **`context.WithValue` to thread the response back**. Rejected: contexts are for cancellation and request-scoped data, not for return values.

**Sources**: <https://pkg.go.dev/sync#Map>, mautrix `pkg/connector/backfill.go` lines 60–80 (the historySyncWakeup pattern is the same shape).

## D2 — FTS5 enablement in `modernc.org/sqlite`

**Decision**: Build with the `sqlite_fts5` build tag enabled. Add `//go:build sqlite_fts5` to the `sqlitehistory` package's main `store.go` file (or use a `_fts5.go` suffix). Configure the schema with `CREATE VIRTUAL TABLE messages_fts USING fts5(body, content='messages', content_rowid='rowid')` and use SQLite triggers to keep the FTS5 index in sync with the canonical `messages` table on insert / update / delete.

**Rationale**: `modernc.org/sqlite` ships FTS5 enabled by default since v1.20.0 (2023). The build-tag check is defensive — it ensures the project fails fast at build time if a future module update changes the default. The contentless-vs-content-table choice (`content='messages'`) avoids data duplication: the FTS5 table only stores tokenised body text and rowid pointers back to the canonical table. Triggers are the standard way to keep them in sync; they cost ~2x write amplification but read queries are O(log n) regardless of message count.

**Alternatives considered**:

- **External FTS via `bleve` or `tantivy`**. Rejected: adds a new top-level dependency, separate index file, separate persistence story. FTS5 is good enough for personal-account scale (~hundreds of thousands of messages max) and lives in the same SQLite file.
- **No full-text search at all** — store messages, scan linearly. Rejected: `wa search "address"` is one of the canonical use cases for the personal assistant; linear scan over 100k+ rows is unacceptable user latency.
- **A separate `sqlite-tantivy` Go binding**. Rejected: CGO required, breaks Constitution Principle IV.

**Sources**: <https://gitlab.com/cznic/sqlite> README (modernc.org/sqlite source), <https://www.sqlite.org/fts5.html> §"External Content Tables".

## D3 — Where the new `HistoryStore` port lives

**Decision**: `internal/app/ports.go` — the same file as the original seven port interfaces from feature 002. Add the `HistoryStore` interface declaration after `AuditLog`, with a doc comment naming the contract clauses (HS1–HS6) it must satisfy.

**Rationale**: CLAUDE.md §"Repository layout" specifies `internal/app/ports.go` as the single file for all port interfaces — "seven interfaces. Resist adding an eighth without a use case demanding it." The use case demanded it (history sync). Splitting `HistoryStore` into a separate file would fragment the discovery experience for new contributors and dilute the "one place to see all the ports" property. The constitution Principle I dependency direction is unchanged.

**Alternatives considered**:

- **`internal/app/history_port.go`** as a sibling file. Rejected: violates "single source of truth" for the port set.
- **A new top-level `internal/app/history/` subpackage**. Rejected: forces use case code to import an extra package boundary for one interface.
- **Defer the port to feature 003.5 or 004**. Rejected by the user during `/speckit:clarify` — they want history in this feature.

**Sources**: [`CLAUDE.md`](../../CLAUDE.md) §"Ports", spec.md FR-022.

## D4 — Mocking the whatsmeow `*Client` for unit tests

**Decision**: Extract a small **package-private** interface `whatsmeowClient` inside the `whatsmeow` adapter package, listing only the ~12 methods the adapter actually calls (`SendMessage`, `IsConnected`, `IsLoggedIn`, `Connect`, `Disconnect`, `Logout`, `GetQRChannel`, `PairPhone`, `Store`, `BuildHistorySyncRequest`, `DownloadHistorySync`, `AddEventHandler`). The `Adapter` struct holds a `whatsmeowClient`, not a `*whatsmeow.Client`. Production constructs from the real client; tests construct from a hand-rolled fake in `client_fake_test.go`.

**Rationale**: This is the Go-idiomatic "accept interfaces, return structs" rule applied at the adapter boundary. The interface lives in the consumer package (the adapter), not in whatsmeow itself, so it has zero impact on upstream. Twelve methods is the right granularity — small enough to fake without ceremony, complete enough to exercise every adapter code path. The fake lives in a `_test.go` file so it does not ship in production builds.

**Alternatives considered**:

- **`gomock` / `mockery` generated mocks**. Rejected: adds a code-generation dependency, the constitution forbids `--no-verify` workflows that hide generated artefacts, and a 100-line hand-rolled fake is more readable than 1000 lines of generated mock with type-assertion gymnastics.
- **Use the real `*whatsmeow.Client` against a recorded VCR-style cassette**. Rejected: WhatsApp's protocol is encrypted with rotating Signal keys; record/replay is structurally impossible.
- **Skip unit tests entirely; rely only on integration tests**. Rejected: integration tests need a real burner number and `WA_INTEGRATION=1`, so unit tests are the only fast feedback loop. Constitution Principle VI mandates port-boundary fakes precisely for this reason.

**Sources**: Dave Cheney, "SOLID Go Design" — https://dave.cheney.net/2016/08/20/solid-go-design ("Accept interfaces, return structs"), and feature 002's [`docs/research-dossiers/hexagonal-antipatterns.md`](../../docs/research-dossiers/hexagonal-antipatterns.md) §7.

## D5 — SQL schema bootstrap via `//go:embed`

**Decision**: The `sqlitehistory` package contains `schema.sql` (the canonical CREATE TABLE + CREATE VIRTUAL TABLE + CREATE TRIGGER statements) and `schema_embed.go` with `//go:embed schema.sql` declaring `var schemaSQL string`. The store constructor runs `db.ExecContext(ctx, schemaSQL)` once on first open, idempotently (the SQL uses `CREATE TABLE IF NOT EXISTS`).

**Rationale**: Embedding the schema via `//go:embed` is the Go 1.16+ canonical pattern for shipping SQL with a package. It keeps the schema visible to anyone reading the directory listing, supports SQL syntax highlighting in editors, and avoids stringly-typed Go code with escaped quotes. Idempotent CREATE statements mean upgrades are no-ops; future schema migrations land in `schema_v2.sql` etc. with a small migration runner.

**Alternatives considered**:

- **Inline string constants**. Rejected: poor readability, no syntax highlighting, escaping nightmares.
- **External migration tool** (`golang-migrate/migrate`, `pressly/goose`). Rejected: overkill for one schema with one table + one virtual table + three triggers; adds a runtime dependency.
- **ORM-driven schema generation** (`gorm`, `ent`). Rejected: every ORM violates the hexagonal core/adapter boundary by leaking ORM-generated types into the application layer. Constitution Principle I forbids it.

**Sources**: <https://pkg.go.dev/embed>, Go 1.16 release notes.

## Phase 0 outcome

Zero `[NEEDS CLARIFICATION]` markers carried into Phase 1. Five tactical decisions resolved with sources. None contradict the spec's `## Clarifications`, the constitution, or CLAUDE.md.

## Sources (consolidated)

- [`spec.md ## Clarifications`](./spec.md) — the architectural decisions for FR-018, FR-019, FR-020
- [`docs/research-dossiers/whatsmeow-history-sync.md`](../../docs/research-dossiers/whatsmeow-history-sync.md) — full mautrix + whatsmeow upstream evidence
- <https://pkg.go.dev/sync#Map> — sync.Map for the request-ID channel
- <https://pkg.go.dev/embed> — go:embed for schema
- <https://www.sqlite.org/fts5.html> — FTS5 external content tables
- <https://gitlab.com/cznic/sqlite> — modernc.org/sqlite README (FTS5 default-on since v1.20)
- Dave Cheney, ["SOLID Go Design"](https://dave.cheney.net/2016/08/20/solid-go-design) — "accept interfaces, return structs"
- [`docs/research-dossiers/hexagonal-antipatterns.md`](../../docs/research-dossiers/hexagonal-antipatterns.md) §7 — interface placement
- [`CLAUDE.md`](../../CLAUDE.md) §"Reliability principles" rule 20 — no fixed port count
