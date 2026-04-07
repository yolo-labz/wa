---
description: "Task list for feature 003-whatsmeow-adapter"
---

# Tasks: whatsmeow Secondary Adapter

**Input**: Design documents from `/specs/003-whatsmeow-adapter/`
**Prerequisites**: [`spec.md`](./spec.md), [`plan.md`](./plan.md), [`research.md`](./research.md), [`data-model.md`](./data-model.md), [`contracts/{historystore,whatsmeow-adapter,sqlitehistory-adapter}.md`](./contracts/), [`quickstart.md`](./quickstart.md)

**Tests**: Required (Constitution Principle VI). Tests are not optional in this project. Unit tests run unconditionally; integration tests run under `//go:build integration` and `WA_INTEGRATION=1`.

**Organization**: Tasks are grouped by user story from spec.md (US1 use-case parity, US2 pairing UX, US3 single-instance store, US4 Renovate loop). Every task includes the absolute file path it produces or modifies. Phase 1 Setup, Phase 2 Foundational, and Phase 7 Polish carry no story label per the strict `/speckit:tasks` format.

## Format: `[ ] [TaskID] [P?] [Story?] Description with absolute file path`

- `[ ]` = pending
- `[P]` = parallel-eligible (different files, no dependency on incomplete tasks)
- `[USn]` = applies only to user-story phases

---

## Phase 1: Setup

**Purpose**: Pre-implementation housekeeping before any source file is written.

- [x] T001 Verify the existing `/Users/notroot/Documents/Code/WhatsAppAutomation/.golangci.yml` `core-no-whatsmeow` `depguard` rule still passes against the empty `internal/adapters/secondary/whatsmeow/` directory by deliberately adding `internal/domain/violation.go` with `import _ "go.mau.fi/whatsmeow"`, running `golangci-lint run ./internal/domain/...`, asserting it fails with the rule name in the output, then deleting `internal/domain/violation.go`
- [x] T002 Delete the placeholder `/Users/notroot/Documents/Code/WhatsAppAutomation/internal/adapters/secondary/whatsmeow/.gitkeep` (in the same commit as the first real `.go` file) <!-- deferred to commit 3 (lands with flags.go) -->
- [ ] T003 Delete the placeholder `/Users/notroot/Documents/Code/WhatsAppAutomation/internal/adapters/secondary/sqlitestore/.gitkeep` (in the same commit as `sqlitestore/store.go`) <!-- deferred to commit 6 -->
- [x] T004 Create directory `/Users/notroot/Documents/Code/WhatsAppAutomation/internal/adapters/secondary/sqlitehistory/` (NEW directory; no `.gitkeep` needed because populated immediately by Phase 5)
- [x] T005 Add `go.mau.fi/whatsmeow` to `/Users/notroot/Documents/Code/WhatsAppAutomation/go.mod` via `go get go.mau.fi/whatsmeow@latest`, then pin the resulting pseudo-version in `/Users/notroot/Documents/Code/WhatsAppAutomation/specs/003-whatsmeow-adapter/research.md` §D11 with the exact pseudo-version, the 40-char SHA, and a one-line `curl ... | grep -E '...'` proving the 12 production client flags from FR-009 still exist on that commit. **Cross-commit execution note** (F1 fix from `/speckit:analyze`): the `go get` part lands in commit 1 (Setup); `go mod tidy` will REMOVE the dep until T011's `flags.go` imports it, so the SHA + grep recording is finalised in commit 3 (US1 adapter), after T011 lands. The task is conceptually one unit but executes across two commits — that is the chicken-and-egg cost of an integration-test-only dependency (part A done; part B in commit 3)

---

## Phase 2: Foundational (Blocking prerequisites)

**Purpose**: The two minimal modifications to feature 002's locked artefacts (`domain.ErrDisconnected` and `app.HistoryStore`), plus the porttest extension. MUST land before any US1 task because everything imports them.

- [x] T006 Add `var ErrDisconnected = errors.New("domain: adapter disconnected")` plus a doc comment to `/Users/notroot/Documents/Code/WhatsAppAutomation/internal/domain/errors.go` per `data-model.md` §"Modification 1"
- [x] T007 Add a single-row table-test entry to `/Users/notroot/Documents/Code/WhatsAppAutomation/internal/domain/errors_test.go` asserting `errors.Is(fmt.Errorf("send: %w", ErrDisconnected), ErrDisconnected)` returns true
- [x] T008 [P] Add the new `HistoryStore` interface declaration with full doc comment (HS1–HS6 contract clauses) to `/Users/notroot/Documents/Code/WhatsAppAutomation/internal/app/ports.go` per `contracts/historystore.md` §"Signature"
- [x] T009 Update `/Users/notroot/Documents/Code/WhatsAppAutomation/CLAUDE.md` §"Ports" by replacing the existing sentence `"Seven interfaces. Resist adding an eighth without a use case demanding it."` with the verbatim replacement: `"Eight interfaces (the original seven from feature 002 plus HistoryStore added by feature 003 for bounded history sync per the procedure in spec.md Edge Cases). Adding a ninth follows the same procedure: amend the relevant feature's spec.md, extend internal/app/porttest/ with a contract test file for the new port, and update this section in the same commit. CLAUDE.md rule 20 (Cockburn: no fixed port count) explicitly permits this."` (F2 fix from `/speckit:analyze` — the original task description used a paraphrase that did not handle the "Resist..." sentence)
- [x] T010 Write `/Users/notroot/Documents/Code/WhatsAppAutomation/internal/app/porttest/historystore.go` extending the existing contract suite with the HS1–HS6 cases from `contracts/historystore.md` §"Behavioural contract", including the `SupportsRemoteBackfill() bool` capability check that conditionally skips HS2 for the in-memory adapter

---

## Phase 3: User Story 1 — Use cases run against a real WhatsApp account (Priority: P1)

**Goal**: The whatsmeow adapter satisfies all eight port interfaces (the original seven + `HistoryStore`) and the contract test suite from `internal/app/porttest/` passes against it.

**Independent test**: With a paired WhatsApp burner number, run `WA_INTEGRATION=1 go test -race -tags integration -run Contract ./internal/adapters/secondary/whatsmeow/...` and observe every contract test case pass. Then run `git diff main..HEAD -- internal/domain internal/app/ports.go` and observe at most one new sentinel line + one new interface block.

- [x] T011 [US1] Write `/Users/notroot/Documents/Code/WhatsAppAutomation/internal/adapters/secondary/whatsmeow/flags.go` with the 12 production whatsmeow client flag constants from FR-009 as `var` declarations or named constants, each with a doc comment naming its purpose and the `mautrix/whatsapp/pkg/connector/client.go` line range it was lifted from
- [x] T012 [P] [US1] Write `/Users/notroot/Documents/Code/WhatsAppAutomation/internal/adapters/secondary/whatsmeow/translate_jid.go` with `toDomain(types.JID) (domain.JID, error)` and `toWhatsmeow(domain.JID) types.JID` (panic-on-zero per the contract) per `contracts/whatsmeow-adapter.md` §"JID translator"
- [x] T013 [P] [US1] Write `/Users/notroot/Documents/Code/WhatsAppAutomation/internal/adapters/secondary/whatsmeow/translate_jid_test.go` with the ~10 test cases from `contracts/whatsmeow-adapter.md` §"Test coverage"
- [x] T014 [US1] Write `/Users/notroot/Documents/Code/WhatsAppAutomation/internal/adapters/secondary/whatsmeow/translate_event.go` with the switch table from `contracts/whatsmeow-adapter.md` §"Translation rules" covering all 8 event types (`Message`, `Receipt`, `Connected`, `Disconnected`, `LoggedOut`, `PairSuccess`, `PairError`, `HistorySyncNotification`) plus the `default` audit-and-skip branch for unknown types per Clarifications round 2 Q2
- [x] T015 [P] [US1] Write `/Users/notroot/Documents/Code/WhatsAppAutomation/internal/adapters/secondary/whatsmeow/translate_event_test.go` with ~15 test cases (one per event type + variant constructors + the unknown-event default branch)
- [x] T016 [US1] Write `/Users/notroot/Documents/Code/WhatsAppAutomation/internal/adapters/secondary/whatsmeow/log.go` with the `slogWALog` bridge type per research §D10 + data-model §"slog → waLog bridge", exported as `NewSlogLogger(*slog.Logger) waLog.Logger` per research §D15
- [x] T017 [US1] Write `/Users/notroot/Documents/Code/WhatsAppAutomation/internal/adapters/secondary/whatsmeow/audit.go` with the `auditRingBuffer` struct (cap 1000), `Record` method satisfying `app.AuditLog`, and `Snapshot` method for tests per `data-model.md` §"Audit ring buffer"
- [x] T018 [P] [US1] Write `/Users/notroot/Documents/Code/WhatsAppAutomation/internal/adapters/secondary/whatsmeow/audit_test.go` with ~5 test cases (record, wrap-around, snapshot defensiveness, parallel writes under -race)
- [x] T019 [US1] Write `/Users/notroot/Documents/Code/WhatsAppAutomation/internal/adapters/secondary/whatsmeow/whatsmeow_client.go` declaring the package-private `whatsmeowClient` interface with the 12 methods per research §D4 + data-model §"whatsmeow.Adapter struct" — the interface that enables the hand-rolled fake
- [x] T020 [P] [US1] Write `/Users/notroot/Documents/Code/WhatsAppAutomation/internal/adapters/secondary/whatsmeow/whatsmeow_client_fake_test.go` (with `_test.go` suffix so it never ships) implementing every method on a `fakeWhatsmeowClient` struct, configurable per-test via setter functions
- [x] T021 [US1] Write `/Users/notroot/Documents/Code/WhatsAppAutomation/internal/adapters/secondary/whatsmeow/adapter.go` with the `Adapter` struct, the `Open(parentCtx, sessionPath, historyPath, allowlist) (*Adapter, error)` constructor following the 9-step Construction order from `data-model.md`, and `Close()` using `errors.Join` per research §D8 + `contracts/whatsmeow-adapter.md` §"Construction"
- [x] T022 [US1] Write `/Users/notroot/Documents/Code/WhatsAppAutomation/internal/adapters/secondary/whatsmeow/send.go` implementing `MessageSender.Send` per `contracts/whatsmeow-adapter.md` §"Send", returning `domain.ErrDisconnected` when `closed=true` or `!IsConnected()`
- [x] T023 [P] [US1] Write `/Users/notroot/Documents/Code/WhatsAppAutomation/internal/adapters/secondary/whatsmeow/send_test.go` with the ~6 cases from `contracts/whatsmeow-adapter.md` §"Test coverage" (happy, ErrDisconnected, ErrEmptyBody, ErrMessageTooLarge, ErrInvalidJID, ctx cancelled)
- [x] T024 [US1] Write `/Users/notroot/Documents/Code/WhatsAppAutomation/internal/adapters/secondary/whatsmeow/stream.go` implementing `EventStream.Next(ctx)` and `Ack(id)` per `contracts/whatsmeow-adapter.md` §"EventStream", reading from the bounded `eventCh` (capacity 256 per Clarifications round 2 Q4)
- [x] T025 [P] [US1] Write `/Users/notroot/Documents/Code/WhatsAppAutomation/internal/adapters/secondary/whatsmeow/stream_test.go` with the ~6 cases (drain, ctx cancelled, clientCtx cancelled, full channel drop, parallel readers, monotonic EventID)
- [x] T026 [P] [US1] Write `/Users/notroot/Documents/Code/WhatsAppAutomation/internal/adapters/secondary/whatsmeow/directory.go` implementing `ContactDirectory.Lookup` and `ContactDirectory.Resolve` against `client.Store.Contacts`, with `ParsePhone` for the `Resolve` path
- [x] T027 [P] [US1] Write `/Users/notroot/Documents/Code/WhatsAppAutomation/internal/adapters/secondary/whatsmeow/groups.go` implementing `GroupManager.List` and `GroupManager.Get` against `client.GetJoinedGroups()` and `client.GetGroupInfo()`
- [x] T028 [P] [US1] Write `/Users/notroot/Documents/Code/WhatsAppAutomation/internal/adapters/secondary/whatsmeow/session.go` implementing `SessionStore.Load`, `Save`, `Clear` by delegating to the `sqlitestore.Container` from Phase 5
- [x] T029 [P] [US1] Write `/Users/notroot/Documents/Code/WhatsAppAutomation/internal/adapters/secondary/whatsmeow/allowlist.go` wrapping `*domain.Allowlist` (the adapter does not own allowlist state; it consumes the daemon's instance)
- [x] T030 [US1] Write `/Users/notroot/Documents/Code/WhatsAppAutomation/internal/adapters/secondary/whatsmeow/history.go` implementing `HistoryStore.LoadMore` per `contracts/historystore.md` §"whatsmeow adapter satisfaction": local-first read from `sqlitehistory.Store`, fall back to `client.BuildHistorySyncRequest` (cap 50 per round-trip) registered in `historyReqs sync.Map`, 30-second `time.NewTimer` `select`, **persist-late, never-leak** semantics per Clarifications round 2 Q1
- [x] T031 [P] [US1] Write `/Users/notroot/Documents/Code/WhatsAppAutomation/internal/adapters/secondary/whatsmeow/history_test.go` with HS1–HS6 cases against the `fakeWhatsmeowClient`, INCLUDING the 30-second timeout test under `testing/synctest.Run` per research §D9
- [x] T032 [US1] Write `/Users/notroot/Documents/Code/WhatsAppAutomation/internal/adapters/secondary/whatsmeow/adapter_integration_test.go` with `//go:build integration` invoking `porttest.RunContractSuite(t, factoryFunc)` from feature 002, gated by `os.Getenv("WA_INTEGRATION")=="1"`. **Must also include a `TestPairRestartReconnect` test** (F8 fix from `/speckit:analyze`) that pairs once via the harness path, calls `adapter.Close()`, re-opens a new `Adapter` against the same session.db, and asserts (a) no QR is printed to stderr and (b) the websocket reaches `events.Connected` within 5 seconds — this is the only automated coverage for spec US2 acceptance scenario 3 and SC-007, since `RunContractSuite` does not exercise the pair-restart path

**Checkpoint**: US1 testable once T011–T032 land. `go test -race ./internal/adapters/secondary/whatsmeow/...` passes against the fake client; `WA_INTEGRATION=1 go test -tags integration` passes the contract suite against a real burner.

---

## Phase 4: User Story 2 — Pairing UX (Priority: P1)

**Goal**: A maintainer can pair a fresh WhatsApp number from a terminal in under 60 seconds (QR default) or 90 seconds (`--pair-phone`).

**Independent test**: Run the pairing harness on a fresh clone with no `~/.local/share/wa/session.db`; scan QR or type the phone code; observe the harness exit 0 and `session.db` appear.

- [x] T033 [US2] Write `/Users/notroot/Documents/Code/WhatsAppAutomation/internal/adapters/secondary/whatsmeow/pair.go` implementing `Adapter.Pair(ctx, phone string)`: empty `phone` triggers the QR-in-terminal flow via `client.GetQRChannel(qrCtx)` with a **3-minute detached `context.WithTimeout(context.Background(), 3*time.Minute)`** per FR-008; non-empty `phone` triggers `client.PairPhone(qrCtx, phone, true, whatsmeow.PairClientChrome, "wad")` per the same FR
- [x] T034 [P] [US2] Write `/Users/notroot/Documents/Code/WhatsAppAutomation/internal/adapters/secondary/whatsmeow/pair_test.go` with ~4 test cases against the fake client (existing session reused, QR flow happy path, phone-code flow happy path, ErrClientLoggedOut emits PairFailure)
- [x] T035 [US2] Create directory `/Users/notroot/Documents/Code/WhatsAppAutomation/internal/adapters/secondary/whatsmeow/internal/pairtest/` and write `main.go` as the manual integration harness — NOT a CLI binary, NOT under `cmd/`. **The file MUST start with `//go:build integration`** so `go build ./...` and `go vet ./...` skip it by default and CI does not produce a stray binary (F3 fix from `/speckit:analyze`). Reads `~/.local/share/wa/session.db` path, opens an `Adapter`, calls `Pair(ctx, phone)`, blocks until paired or `ctx.Done()`, exits 0 on success per spec FR-016 and the Pairing Harness Key Entity definition. Build via `go build -tags integration ./internal/adapters/secondary/whatsmeow/internal/pairtest/...`
- [x] T036 [P] [US2] Verify the pairing harness compiles via `go build ./internal/adapters/secondary/whatsmeow/internal/pairtest/...` (no execution, no real WhatsApp call)
- [x] T037 [US2] Document the manual pairing procedure in `/Users/notroot/Documents/Code/WhatsAppAutomation/specs/003-whatsmeow-adapter/quickstart.md` step 6 (already present from `/speckit:plan`); verify the doc still matches the implemented harness path

**Checkpoint**: US2 testable once T033–T037 land. A maintainer with a burner phone can pair end-to-end in under 60 seconds.

---

## Phase 5: User Story 3 — Single-instance store (Priority: P1)

**Goal**: Two adapter instances cannot corrupt the SQLite ratchet store; both `session.db` and `messages.db` are independently `flock`'d via `rogpeppe/go-internal/lockedfile`.

**Independent test**: Start one pairing harness; start a second; observe the second fail with "session locked" within 100ms without touching `session.db` or `messages.db`.

- [X] T038 [US3] Write `/Users/notroot/Documents/Code/WhatsAppAutomation/internal/adapters/secondary/sqlitestore/store.go` wrapping `whatsmeow/sqlstore.Container` with a `lockedfile.Edit` lock on `session.db.lock`, implementing `Open(ctx, path) (*Container, error)` and `Close() error`. Sets `0700` on the parent dir, `0600` on the database file, uses `modernc.org/sqlite` via the `whatsmeow/sqlstore` driver hook
- [X] T039 [P] [US3] Write `/Users/notroot/Documents/Code/WhatsAppAutomation/internal/adapters/secondary/sqlitestore/store_test.go` with ~3 test cases (single open OK, second open fails, after Close second Open OK) per `contracts/whatsmeow-adapter.md` §"flock contention" coverage matrix
- [X] T040 [P] [US3] Write `/Users/notroot/Documents/Code/WhatsAppAutomation/internal/adapters/secondary/sqlitestore/doc.go` with the package doc explaining the schema is whatsmeow's, not ours, and that this package only provides the Container + lock
- [x] T041 [US3] Write `/Users/notroot/Documents/Code/WhatsAppAutomation/internal/adapters/secondary/sqlitehistory/schema.sql` with the verbatim CREATE TABLE + CREATE VIRTUAL TABLE FTS5 + 3 sync triggers from `data-model.md` §"SQL schema", terminating with `PRAGMA user_version = 1` for the migration runner
- [x] T042 [P] [US3] Write `/Users/notroot/Documents/Code/WhatsAppAutomation/internal/adapters/secondary/sqlitehistory/schema_embed.go` with `//go:embed schema.sql` declaring `var schemaSQL string` per research §D5
- [x] T043 [US3] Write `/Users/notroot/Documents/Code/WhatsAppAutomation/internal/adapters/secondary/sqlitehistory/store.go` with `Open`, `Close`, `LoadMore`, `Insert`, `Search` methods per `contracts/sqlitehistory-adapter.md`, using `lockedfile.Edit` for the second flock, `modernc.org/sqlite` driver, the four PRAGMAs from Clarifications round 2 Q5, and the gzipped `raw_proto BLOB` round-trip per data-model §"Schema invariants"
- [x] T044 [P] [US3] Write `/Users/notroot/Documents/Code/WhatsAppAutomation/internal/adapters/secondary/sqlitehistory/store_test.go` with the ~12 cases from `contracts/sqlitehistory-adapter.md` §"Test coverage": schema bootstrap idempotency, `LoadMore` happy paths, `Insert` idempotency, `Search` FTS5 (literal, prefix, multi-word, accent insensitivity for `endereco`/`endereço`, no match, limit), parallel `LoadMore` + `Insert` under `-race`
- [x] T045 [US3] Write `/Users/notroot/Documents/Code/WhatsAppAutomation/internal/adapters/secondary/sqlitehistory/flock_test.go` with 3 cases (single open OK, second open fails, after Close second Open OK) — analogous to T039 for the second database file

**Checkpoint**: US3 testable once T038–T045 land. `go test -race ./internal/adapters/secondary/sqlitestore/... ./internal/adapters/secondary/sqlitehistory/...` passes; both `flock` contention tests pass.

---

## Phase 6: User Story 4 — Renovate loop (Priority: P2)

**Goal**: A whatsmeow Renovate bump opens a PR whose body contains the upstream commit range and whose CI run exercises the contract suite, surfacing protocol breakage immediately.

**Independent test**: After this feature merges, observe the next Renovate PR's body for the commit range and confirm CI runs.

- [ ] T046 [US4] Verify the existing `/Users/notroot/Documents/Code/WhatsAppAutomation/renovate.json` `whatsmeow` package rule is unchanged from feature 001 (`schedule: at any time`, `semanticCommitType: fix`, `fetchChangeLogs: branch`, `commitMessageTopic: whatsmeow`); no edit if already correct
- [ ] T047 [US4] Document the bump-validate cycle in a new `/Users/notroot/Documents/Code/WhatsAppAutomation/docs/runbooks/whatsmeow-bump.md` (NEW file): on Renovate PR receipt, read the upstream commit range, run `go test ./...`, run `WA_INTEGRATION=1 go test -tags integration -run Contract ./internal/adapters/secondary/whatsmeow/...` if a burner is available, merge if green or block-and-investigate if red. (F4 fix from `/speckit:analyze`: the runbook is the operational backstop for FR-015 — the FR mandates the Renovate package rule remain active; this task creates the maintainer-facing documentation that turns "rule remains active" into "maintainer knows what to do when the rule fires")

---

## Phase 7: Polish & cross-cutting

- [ ] T048 Run `go test -race -count=1 ./...` across the whole module; record the wall time and assert it stays under SC-008's "under 5 seconds for unit tests" target
- [ ] T049 Run `golangci-lint run ./...` with the v2.x config; assert zero findings (the `core-no-whatsmeow` `depguard` rule active)
- [ ] T050 Run `go vet ./...`; assert zero findings
- [ ] T051 Run the deliberate-violation depguard test from `quickstart.md` §3 against the new adapter packages; assert the rule still fires
- [ ] T052 Run `find /Users/notroot/Documents/Code/WhatsAppAutomation/internal/adapters/secondary/{whatsmeow,sqlitestore,sqlitehistory} -name '*.go' -not -name '*_test.go' | xargs wc -l` and assert the total stays under 2200 LOC per SC-008 (the per-file budget table in `data-model.md` §"Per-file LOC budget" sums to ~2120)
- [ ] T053 Tick CHK038–CHK045 in `/Users/notroot/Documents/Code/WhatsAppAutomation/specs/003-whatsmeow-adapter/checklists/requirements.md` after the corresponding deliverables land
- [ ] T054 Execute steps 1–5 of `/Users/notroot/Documents/Code/WhatsAppAutomation/specs/003-whatsmeow-adapter/quickstart.md` end-to-end (no burner needed); assert every block exits 0
- [ ] T055 (Burner-only, manual) Execute steps 6–9 of `quickstart.md` with a paired WhatsApp burner number; assert the contract suite passes against the real adapter and the bounded history sync produces a single-digit-MB `messages.db`
- [ ] T056 Add `/Users/notroot/Documents/Code/WhatsAppAutomation/internal/adapters/secondary/whatsmeow/reconnect_bench_test.go` with a `BenchmarkReconnectLatency` (or `TestReconnectLatency` with `t.Logf` of measured wall time) against the `fakeWhatsmeowClient` measuring time-from-`Disconnect`-to-`events.Connected` translation. Used to validate spec SC-007 ("reconnect after restart <5s") in a deterministic way; the burner-only manual path in T055 remains the real-world verification (F5 fix from `/speckit:analyze`)
- [ ] T057 Commit each phase as a single conventional-commit per the boundary table below and push to `origin/003-whatsmeow-adapter`
- [ ] T058 Tag `v0.0.3-whatsmeow-adapter` (annotated) marking the end of feature 003

---

## Dependencies

```text
T001..T005 (Setup) ──▶ T006..T010 (Foundational) ──┐
                                                    ├──▶ T011..T032 (US1)
                                                    │             │
                                                    │             ├──▶ T033..T037 (US2 — depends on adapter.go from T021)
                                                    │             │
                                                    │             └──▶ depends on T038..T045 (US3) for sqlitestore/sqlitehistory
                                                    │
                                                    │       T038..T045 (US3 — sqlitestore + sqlitehistory)
                                                    │             │
                                                    │             ▼
                                                    │       T046..T047 (US4 — Renovate loop docs)
                                                    │             │
                                                    │             ▼
                                                    └──▶ T048..T057 (Polish)
```

- T005 (pin SHA + grep proof) gates T011 (which sets the 12 client flags whose existence the grep proves)
- T008 (HistoryStore interface) and T010 (porttest extension) gate T030 (HistoryStore impl) and T031 (history_test.go)
- T038 (sqlitestore) and T043 (sqlitehistory) gate T021 (Adapter constructor) because Open() opens both stores
- T021 (adapter.go) gates T022..T032 because they all live on the Adapter struct
- T033 (pair.go) depends on T021 (Adapter struct exists)
- T035 (pairtest harness) depends on T033 (Pair method exists)
- T032 (integration test) depends on T021..T031, T033, T038, T043

## Parallel example

Inside Phase 3 (US1), tasks T012/T013, T015, T018, T020, T023, T025, T026, T027, T028, T029, T031 are all `[P]` because each touches a disjoint file. T011 (flags), T014 (translate_event), T016 (log), T017 (audit), T019 (interface), T021 (adapter), T022 (send), T024 (stream), T030 (history), T032 (integration test) are sequential against each other only where they share the Adapter struct or interface declaration.

Inside Phase 5 (US3), T039, T040, T042, T044 are `[P]` against each other; the SQL writes (T038, T041, T043, T045) are sequential against their respective stores.

## Implementation strategy

**MVP scope** for feature 003 = User Stories 1, 2, 3 (Phases 3–5). US4 (Renovate loop documentation) is closure, not blocking.

The natural commit boundaries:

| Commit | Phase coverage | Conventional message |
|---|---|---|
| 1 | T001–T005 + T009 (CLAUDE.md ports bump) | `chore: bump CLAUDE.md ports count and pin whatsmeow SHA for feature 003` |
| 2 | T006–T008, T010 | `feat(domain,app): add ErrDisconnected sentinel + HistoryStore port` |
| 3 | T011–T020 | `feat(adapter/whatsmeow): add JID + event translators, flags, audit ring, fake client` |
| 4 | T021–T032 | `feat(adapter/whatsmeow): add Adapter constructor + 7 port impls + history` |
| 5 | T033–T037 | `feat(adapter/whatsmeow): add pairing flow + manual harness` |
| 6 | T038–T040 | `feat(adapter/sqlitestore): add whatsmeow ratchet store wrapper with lockedfile` |
| 7 | T041–T045 | `feat(adapter/sqlitehistory): add messages.db with FTS5 + lockedfile` |
| 8 | T046–T047 | `docs(runbooks): add whatsmeow Renovate bump procedure` |
| 9 | T048–T058 | `chore(test): polish — full test, lint, vet, depguard, quickstart, reconnect benchmark, tag v0.0.3` |

9 commits total. The order respects the dependency graph: stores must exist before the Adapter constructor can open them; the Adapter must exist before pairing can call into it.

## Out of scope (deferred to feature 004+)

- Daemon process (`cmd/wad/main.go`) — feature 004
- CLI client (`cmd/wa/main.go`) — feature 005
- JSON-RPC unix socket server — feature 004
- Rate limiter middleware, warmup ramp, allowlist file watcher, audit log file writer — feature 004
- `<channel source="wa">` tag wrapper — feature 005 (or the future plugin layer)
- The `wa-assistant` Claude Code plugin (separate repo `yolo-labz/wa-assistant`) — feature 007
- GoReleaser pipeline + macOS notarization + Homebrew tap + Nix flake build — feature 006
- Schema migration runner beyond `schema.sql v1` — first feature that needs `schema_v2.sql`
- 8th-or-later additional ports — same procedure as documented in feature 002 spec.md "Edge Cases"
