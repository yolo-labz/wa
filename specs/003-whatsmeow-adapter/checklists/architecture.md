# Architecture Decision Quality Checklist: 003-whatsmeow-adapter

**Purpose**: Unit-test the **architectural and tooling decisions** documented across [`spec.md`](../spec.md), [`plan.md`](../plan.md), [`research.md`](../research.md), [`data-model.md`](../data-model.md), and [`contracts/{historystore,whatsmeow-adapter,sqlitehistory-adapter}.md`](../contracts/) for **completeness, clarity, consistency, measurability, modernity, and traceability**. This is a "unit test for English" pass over the *design quality* of feature 003, independent from `requirements.md` (which validates spec hygiene and deliverable presence).

**Created**: 2026-04-07
**Feature**: feature 003 — whatsmeow secondary adapter
**Theme**: design patterns, modern Go tooling, hexagonal layering, history sync architecture, two-database split, contract suite extension
**Depth**: deep ambiguity hunt with explicit "is this still 2026 best practice?" lens
**Audience**: an external maintainer evaluating whether to start `/speckit:implement` against this spec, AND whether the chosen tools are still current

> Each item asks whether the design choices are *defended with 2026 evidence* in the spec, not whether the implementation exists. References use `[spec FR-NNN]`, `[plan §X]`, `[research §Dn]`, `[data-model §Y]`, `[contracts/<file>]`, `[CLAUDE.md §V]`, `[constitution §K]`, or quality markers `[Gap]`, `[Ambiguity]`, `[Conflict]`, `[Assumption]`, `[Modernity]`.

## Library and dependency currency

- [x] CHK001 Is `go.mau.fi/whatsmeow` confirmed as the still-best WhatsApp multi-device library in 2026, with evidence (commit cadence, maintainer, alternative comparison) cited in research or the dossier? [Modernity] [`research §"sources"`] [`docs/research-dossiers/whatsmeow-history-sync.md`]
- [x] CHK002 Is the choice of `modernc.org/sqlite` over alternatives (`ncruces/go-sqlite3` WASM-based, `crawshaw/sqlite` CGO, `mattn/go-sqlite3` CGO) justified with a 2026 comparison rather than asserted? [Modernity] [`research §D2`] [Gap]
- [ ] CHK003 Is the `sqlite_fts5` build tag still required in modernc.org/sqlite latest, or has FTS5 become default? [Ambiguity] [`research §D2`] [`contracts/sqlitehistory-adapter.md` §"Open sequence"]
- [ ] CHK004 Is `mdp/qrterminal/v3` cited as the current best terminal-QR library in 2026, with rejected alternatives noted? [Modernity] [`spec FR-008`] [Gap]
- [ ] CHK005 Are the 12 production whatsmeow client flags from `mautrix/whatsapp/pkg/connector/client.go` (per FR-009) verified as still-current against the latest whatsmeow commit, not just copied from a 2024 reference? [Modernity] [`spec FR-009`] [`data-model §"Construction order"`]

## SQLite + FTS5 architecture

- [x] CHK006 Is the trigger-based content-table FTS5 sync pattern (`content='messages'` + INSERT/DELETE/UPDATE triggers) the 2026-recommended approach, or has SQLite 3.44+ introduced a better contentless-delete option? [Modernity] [`data-model §"SQL schema"`] [`contracts/sqlitehistory-adapter.md`]
- [x] CHK007 Is the schema's `tokenize='unicode61 remove_diacritics 2'` choice defended with the Brazilian-Portuguese accent-insensitive search requirement, AND is the `2` argument's meaning documented? [Clarity] [`data-model §"SQL schema"`] [`contracts/sqlitehistory-adapter.md`]
- [ ] CHK008 Are the SQLite PRAGMAs (`journal_mode(WAL)`, `synchronous(NORMAL)`, `foreign_keys(ON)`, `busy_timeout(5000)`) defended individually with rationale, or asserted as a block? [Clarity] [`contracts/sqlitehistory-adapter.md` §"Open sequence"]
- [ ] CHK009 Is the `raw_proto BLOB` storage of gzipped whatsmeow protobuf documented with both the rationale (round-trip preservation for future re-translation) AND the cost (write amplification, decode latency)? [Completeness] [`data-model §"SQL schema"`]
- [ ] CHK010 Is the schema migration story spelled out (e.g., `schema_v2.sql` with a small migration runner) or only mentioned as deferred? [Coverage] [`data-model §"What this data model is NOT"`] [Gap]
- [x] CHK011 Is the choice of `sqlc` vs hand-rolled SQL evaluated, given that the adapter has ~6 queries and `sqlc` is the 2026 idiom for type-safe SQL in Go? [Modernity] [Gap]

## Concurrency primitives

- [x] CHK012 Is the `sync.Map[string]chan` choice for request-ID-keyed history responses defended against alternatives (`sync.RWMutex` + `map`, `puzpuzpuz/xsync.MapOf`, generic typed channel registry) with 2026 evidence? [Modernity] [`research §D1`]
- [ ] CHK013 Does the spec specify the cleanup contract for the `historyReqs` `sync.Map` — when entries are deleted, what happens on duplicate response, what happens on context-deadline expiry mid-wait? [Completeness] [`research §D1`] [`contracts/historystore.md`] [Gap]
- [ ] CHK014 Is the `eventCh` channel capacity (100) defended with throughput math, or asserted? [Measurability] [`data-model §"whatsmeow.Adapter struct"`] [`contracts/whatsmeow-adapter.md` §"handleWAEvent"]
- [x] CHK015 Is the "drop on full channel + audit log" strategy for `eventCh` overflow defended as the right call vs (a) blocking the whatsmeow handler, (b) growing the buffer, (c) backpressuring the websocket? [Completeness] [`contracts/whatsmeow-adapter.md` §"handleWAEvent"]
- [x] CHK016 Is the `flock` primitive choice (`syscall.Flock` vs `gofrs/flock` cross-platform vs `rogpeppe/go-internal/lockedfile` Go-toolchain-blessed) defended with rationale? [Modernity] [`spec FR-007`] [Gap]
- [x] CHK017 Are the **two flocks** (one per database file) acquired in a documented order with deadlock-avoidance reasoning? [Edge Cases] [`contracts/whatsmeow-adapter.md` §"Construction"] [`data-model §"Construction order"`]

## whatsmeow client setup

- [x] CHK018 Is `client.ManualHistorySyncDownload = true` the only flag set BEFORE `Connect()`, or are the 12 production flags also set pre-Connect, with the order documented? [Completeness] [`data-model §"Construction order"`]
- [x] CHK019 Is the `DeviceProps.HistorySyncConfig{Days: 7, Size: 20MB, Quota: 100MB}` mutation tied to a specific point in the lifecycle (before/after device load, before Connect) with the consequences of getting it wrong stated? [Clarity] [`spec FR-019`] [`data-model §"Construction order"`]
- [x] CHK020 Is the `whatsmeowClient` interface's method list (research §D4) exhaustive enough that every code path in the adapter compiles against the fake without escape hatches? [Completeness] [`research §D4`] [`data-model §"Adapter struct"`]
- [x] CHK021 Is the choice of hand-rolled fake over `vektra/mockery` v2/v3 or `go.uber.org/mock` (the revived gomock) defended with a comparison, or asserted by appeal to a Cheney post? [Modernity] [`research §D4`]

## Translation boundary (JID + event)

- [x] CHK022 Does `translate_jid.go`'s panic-on-zero-JID rule have a documented justification (programmer error vs runtime safety) and at least one test that asserts the panic? [Clarity] [`contracts/whatsmeow-adapter.md` §"JID translator"]
- [x] CHK023 Are all 8+ inbound whatsmeow `events.*` types listed in FR-004 mapped 1:1 to a domain `Event` variant in the contracts, with no whatsmeow event silently dropped without an audit-log row? [Coverage] [`spec FR-004`] [`contracts/whatsmeow-adapter.md` §"Translation rules"]
- [ ] CHK024 Is the "unknown event type → silent return" branch in `handleWAEvent` defended? Should unknown events be logged to the audit log? [Edge Cases] [`contracts/whatsmeow-adapter.md` §"handleWAEvent"]
- [x] CHK025 Is the `events.QR` exclusion from the event handler (handled separately by `GetQRChannel`) documented with the rationale? [Clarity] [`contracts/whatsmeow-adapter.md` §"Translation rules"]

## History sync architecture (the load-bearing decision)

- [x] CHK026 Is the **bound-at-source** pattern (`DeviceProps.HistorySyncConfig`) defended over alternatives like client-side filtering or post-download truncation? [Coverage] [`spec ## Clarifications`] [`docs/research-dossiers/whatsmeow-history-sync.md`]
- [x] CHK027 Is the **30-second timeout** on the on-demand `BuildHistorySyncRequest` round-trip defended with the mautrix-observed value, or asserted? [Measurability] [`research §D1`] [`contracts/historystore.md` §"whatsmeow adapter satisfaction"]
- [x] CHK028 Is the **50-message-per-round-trip cap** on `BuildHistorySyncRequest` documented as phone-enforced (not adapter-enforced) so a future contributor cannot raise it without verifying the upstream? [Clarity] [`contracts/historystore.md`]
- [ ] CHK029 Is the procedure for handling a `BuildHistorySyncRequest` response that arrives **after** the 30s timeout (orphan blob) defined? [Edge Cases] [Gap]
- [ ] CHK030 Is the rule "drop `INITIAL_STATUS_V3` and `FULL` SyncTypes" defended with the spec rationale, or asserted? Should `FULL` be reconsidered if a future user wants archive search? [Clarity] [`spec FR-019`] [Gap]

## Test strategy

- [x] CHK031 Is the contract suite extension `internal/app/porttest/historystore.go` defined to be invokable by BOTH the in-memory adapter (HS3 path) AND the whatsmeow adapter (HS2 path), with a capability check that conditionally skips HS2? [Coverage] [`contracts/historystore.md` §"In-memory adapter satisfaction"]
- [x] CHK032 Is the integration test gating (`//go:build integration` + `WA_INTEGRATION=1`) consistent with feature 002's gating, so a single command runs both feature 002 and feature 003 integration suites? [Consistency] [`spec FR-013`]
- [x] CHK033 Is `testing/synctest` (Go 1.24+ deterministic-time framework) considered for the 30-second timeout test, or is real time acceptable? [Modernity] [Gap]
- [x] CHK034 Are tests for the FTS5 accent-insensitive search documented as required (e.g., search "endereco" matches "endereço")? [Coverage] [`contracts/sqlitehistory-adapter.md` §"Test coverage"]
- [x] CHK035 Is the "deliberate violation" depguard test from feature 002's quickstart re-runnable on feature 003's branch as a regression check? [Consistency] [`quickstart.md §3`]

## Constitution and CLAUDE.md alignment

- [x] CHK036 Is the SC-002 softening (allowing one new sentinel + one new interface) tied explicitly to CLAUDE.md rule 20 ("ports as intent of conversation, no fixed port count") AND to the procedure in feature 002 spec.md "Edge Cases"? [Consistency] [`spec SC-002`] [`spec FR-022`] [`CLAUDE.md §"Reliability principles"`]
- [x] CHK037 Is the Principle III "PARTIAL — JUSTIFIED" verdict in plan.md tied to the same justification feature 002 used (rate limiter is feature 004's middleware, not feature 003's adapter)? [Consistency] [`plan §"Constitution Check"`]
- [x] CHK038 Does the spec re-cite the `core-no-whatsmeow` `depguard` rule by name in spec, plan, AND ports.go's doc comment for the new `HistoryStore` interface? [Consistency] [`spec FR-002`] [`plan §"Constitution Check"`] [`data-model §"Modification 2"`]
- [ ] CHK039 Is the eighth-port addition documented in the SAME commit as a CLAUDE.md update if the new port affects rule 21 (port set completeness test)? [Edge Cases] [`spec ## Clarifications`] [`CLAUDE.md §"Reliability principles"`] [Gap]

## Modern Go idioms

- [x] CHK040 Is `errors.Join` (Go 1.20+) considered for the case where Adapter.Close encounters errors from BOTH `history.Close()` AND `store.Close()`? [Modernity] [`data-model §"Close order"`] [Gap]
- [x] CHK041 Is `log/slog` (stdlib since 1.21) wired to whatsmeow's `waLog` interface, or does the adapter use a third-party logger? [Modernity] [Gap]
- [x] CHK042 Is `//go:embed` (Go 1.16+) the canonical 2026 way to ship the SQL schema, or has anything newer (e.g., `sqlc`) emerged that subsumes embedding? [Modernity] [`research §D5`]
- [x] CHK043 Is the `atomic.Uint64` for `eventSeq` and `atomic.Bool` for `closed` (Go 1.19 typed atomics) used instead of `sync/atomic.LoadUint64` patterns? [Modernity] [`data-model §"Adapter struct"`]
- [ ] CHK044 Are the JSON-RPC error code ranges (deferred to feature 004) referenced from this feature's audit-log AuditAction definitions, so the daemon can map adapter errors to RPC codes without re-parsing? [Coverage] [Gap]

## Cross-document consistency

- [x] CHK045 Does `data-model.md`'s adapter struct field list exactly match `contracts/whatsmeow-adapter.md`'s "Adapter struct" section? [Consistency] [`data-model §"Adapter struct"`] [`contracts/whatsmeow-adapter.md` §"Construction"]
- [x] CHK046 Does `quickstart.md` step 4's required-files list exactly match the `## Project Structure` tree in `plan.md`? [Consistency] [`quickstart.md §4`] [`plan §"Project Structure"`]
- [ ] CHK047 Is the LOC budget (~2200 from SC-008) tied to a per-file estimate that adds up to ~2200, or is the total asserted? [Measurability] [`spec SC-008`] [Gap]
- [ ] CHK048 Are the 11 RPC methods from CLAUDE.md §"Daemon, IPC, single-instance" updated to 12 (adding `history`) in this feature's spec, OR is the update deferred to feature 004 with a TODO marker? [Coverage] [`spec FR-022`] [Gap]

## Notes

- This checklist has 48 items across 10 categories. Soft cap from `/speckit:checklist` rules is 40; the +8 overflow is justified by the breadth of the modernity audit (the user explicitly asked for "modern tools and all that jazz").
- Items marked `[Modernity]` test whether the spec defends a tool/pattern choice against 2026 alternatives, not just whether the choice is internally consistent.
- Items marked `[Gap]` are missing decisions or under-specified ones; closing them requires editing the spec/plan/research/contracts.
- A research agent (`abe58ef92e0443add`, deep-researcher) is running in parallel to validate the modernity claims (CHK001–CHK005, CHK011, CHK016, CHK021, CHK033, CHK040–CHK043). When it returns, this checklist is updated with the agent's findings: items become `[x]` if confirmed, `[ ]` with a note if revised.
- Re-run after `/speckit:implement` lands the actual code: items measuring "is X defended in the spec" become "is X correctly implemented in the code".
