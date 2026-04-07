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
- **`ncruces/go-sqlite3`** (WASM-based pure-Go SQLite, 2-4× faster on read-heavy workloads per the project README). Rejected: smaller production mileage than `modernc.org/sqlite` (which is used by `caddy`, `gitea`); the perf delta is irrelevant for ~10 RPS daemon traffic; FTS5 is opt-in via build tag and the driver API surface differs. Re-evaluate if the project ever needs sub-millisecond search latency. <https://github.com/ncruces/go-sqlite3>
- **`crawshaw/sqlite`** (CGO). Rejected: violates Constitution Principle IV.
- **`sqlc`** (https://sqlc.dev) — type-safe Go from `.sql` files, supports `modernc.org/sqlite` since v1.20 (2024). Rejected for v0: the adapter has ~6 queries, well below the ~20-query break-even where `sqlc` saves more boilerplate than the codegen toolchain costs to maintain. Reconsider if the adapter's query count exceeds 15.

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

- **`go.uber.org/mock`** (the maintained fork of `golang/mock` since 2023, commonly called "the new gomock"). Rejected: generated mocks become a maintenance burden when the interface is small and stable; the 12-method `whatsmeowClient` interface is exactly the case where a hand-rolled fake is shorter than the `mockgen` invocation in the Makefile. CLAUDE.md anti-pattern #5 ("Mock-everything tests. Prefer in-memory fakes") is explicit. <https://github.com/uber-go/mock>
- **`vektra/mockery` v3** (2025, added type-parameter support). Same critique as `go.uber.org/mock`; rejected for the same reason.
- **`testify/mock`**. Rejected: brings in the entire `testify` framework as a dependency, which violates the "stdlib `testing` only" rule the project inherited from feature 002 (`contracts/ports.md` §"Forbidden patterns" lists "no testify" explicitly).
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

## D6 — File-locking primitive: `rogpeppe/go-internal/lockedfile`

**Decision**: Use `github.com/rogpeppe/go-internal/lockedfile`, NOT raw `syscall.Flock` or `gofrs/flock`. Both `sqlitestore` and `sqlitehistory` acquire their per-file lock via `lockedfile.Edit(path)` (the same primitive the Go toolchain uses for module-cache locking).

**Rationale**: `lockedfile` is what `cmd/go` itself uses, handles darwin's `O_EXLOCK` quirks, linux `flock` semantics, and the Windows `LockFileEx` path if cross-compilation ever matters. It is already a transitive dependency through `rogpeppe/go-internal/testscript` (which the v0 testing strategy mandates per CLAUDE.md `v0 testing strategy` §4). Direct `syscall.Flock` is darwin/linux-only; `gofrs/flock` is a thinner wrapper without the Go-toolchain pedigree.

**Alternatives considered**:

- **`syscall.Flock`** (originally proposed in this feature's spec FR-007). Rejected: darwin/linux-only, no Windows path, and Go is steadily deprecating direct `syscall` usage in favour of `golang.org/x/sys/unix`.
- **`gofrs/flock`**. Rejected: thinner wrapper without the Go-toolchain pedigree; smaller test surface.
- **In-process `sync.Mutex`**. Rejected: does not protect against a second process touching the file.

**Sources**: <https://pkg.go.dev/github.com/rogpeppe/go-internal/lockedfile>, <https://github.com/golang/go/tree/master/src/cmd/go/internal/lockedfile> (the Go toolchain's own usage), modernity dossier `docs/research-dossiers/whatsmeow-adapter-modernity.md` §4.

## D7 — FTS5: trigger-synced content table vs `contentless_delete=1`

**Decision**: Use the **trigger-synced content-table** pattern (`content='messages'` + AFTER INSERT/DELETE/UPDATE triggers), NOT `contentless_delete=1`. This is the pattern in `data-model.md §"SQL schema"`.

**Rationale**: `contentless_delete=1` (SQLite 3.44+) lets you `DELETE` from a contentless FTS5 index without storing original tokens, which is faster for write-heavy ingestion. But our access pattern is **read-heavy** (search the assistant's history) with **bursty writes** (history sync delivery in batches), and we WANT the original `body` column queryable directly without going through the FTS5 index for non-search reads. The trigger-synced pattern keeps `messages.body` as the canonical text and `messages_fts` as a pure index — best of both worlds at the cost of ~2x write amplification. For ~hundreds of writes/sec peak, the cost is invisible.

**Alternatives considered**:

- **`contentless_delete=1` with `content=''`**. Rejected: forces every read of the message body to go through the FTS5 index, which loses the original text (only tokens stored). Our `domain.Message.Body` field needs the original text; this would force a separate column anyway.
- **External FTS via `bleve`**. Rejected in research §D2 — adds a separate index file and persistence story; same reasoning applies here.

**Sources**: <https://sqlite.org/fts5.html#contentless_delete_tables>, modernity dossier §3.

## D8 — `errors.Join` for multi-resource cleanup

**Decision**: `Adapter.Close()`, `sqlitestore.Store.Close()`, and `sqlitehistory.Store.Close()` MUST use `errors.Join` (Go 1.20+) when releasing multiple resources, so that a failure in releasing the history-store flock does not hide a failure in releasing the session-store flock (and vice versa).

**Rationale**: The `Adapter.Close()` sequence in `data-model.md §"Close order"` releases two flocks, closes two SQL handles, drains a channel, and cancels a context. If any of these fails, the caller needs to see all the failures, not just the first one swallowed by a single-error return. `errors.Join` is exactly the stdlib primitive for this.

**Alternatives considered**:

- **Single `fmt.Errorf("%w: ...")` chain**. Rejected: only carries one error; the others are lost.
- **`go.uber.org/multierr`**. Rejected: third-party dependency for a stdlib feature.
- **First-error-wins**. Rejected: hides the second flock failure, which can mask the actual root cause.

**Sources**: <https://pkg.go.dev/errors#Join>, Go 1.20 release notes, modernity dossier §11.

## D9 — `testing/synctest` for the 30-second on-demand history timeout test

**Decision**: The unit test for the `HistoryStore.LoadMore` 30-second timeout (per `contracts/historystore.md §"whatsmeow adapter satisfaction"` step 5) MUST use `testing/synctest` (Go 1.24+, stable since Feb 2025), NOT real-time `time.Sleep`. The test runs under `synctest.Run(func(t *testing.T) { ... })` so virtual time advances on `time.Sleep` and `time.NewTimer`, making the test deterministic and ~instantaneous.

**Rationale**: A 30-second wall-clock test is unacceptable in CI (`go test -count=10` becomes 5 minutes per run). `testing/synctest` was designed precisely for this. The project's `go.mod` declares `go 1.26.1` so Go 1.24+ is available.

**Alternatives considered**:

- **Real `time.Sleep` in tests**. Rejected: 30 seconds × N iterations × parallel test runs = unacceptable CI cost.
- **`clockwork` or `jonboulle/clockwork`** (third-party fake clock). Rejected: stdlib `testing/synctest` covers the use case without an extra dep.
- **Inject a clock interface and pass a fake**. Rejected: the request-ID-keyed channel logic in research §D1 uses `time.NewTimer` directly; injecting a clock here would require restructuring the channel select for one test.

**Sources**: <https://pkg.go.dev/testing/synctest>, Go 1.24 release notes, modernity dossier §12.

## D10 — `waLog.Logger` → `log/slog` bridge type

**Decision**: The `whatsmeow` adapter package contains a small bridge type `slogWALog` implementing whatsmeow's `waLog.Logger` interface (`Debugf/Infof/Warnf/Errorf/Sub`) by delegating to `*slog.Logger`. The bridge lives in `internal/adapters/secondary/whatsmeow/log.go` and is constructed once in `Open()` from the `*slog.Logger` the daemon (feature 004) passes via `Open` arguments.

**Rationale**: CLAUDE.md §"Locked decisions" mandates `log/slog` (stdlib since 1.21) + `lmittmann/tint` for dev. whatsmeow exposes `waLog.Logger` for its internal logging. The bridge is the single canonical adapter so contributors don't reinvent it per file. Naming it explicitly in `data-model.md` (CHK041 in the architecture checklist) prevents the "two implementations of the same bridge" anti-pattern.

**Alternatives considered**:

- **Use whatsmeow's built-in `waLog.Stdout`**. Rejected: ignores CLAUDE.md's slog mandate; loses structured logging.
- **Wrap `lmittmann/tint` directly in production**. Rejected: dev concern; production should use plain JSON slog handler.
- **Skip whatsmeow logging entirely**. Rejected: protocol breakage debugging needs whatsmeow's debug output.

**Sources**: <https://pkg.go.dev/log/slog>, <https://pkg.go.dev/go.mau.fi/whatsmeow/util/log>, modernity dossier §13, [`CLAUDE.md`](../../CLAUDE.md) §"Locked decisions" logging row.

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
