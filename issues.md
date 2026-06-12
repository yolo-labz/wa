# issues.md — yolo-labz/wa code audit

- **Compiled:** 22/04/2026
- **Ref commit:** 2543d78 (post-PR #31, v2.0.0-rc1)
- **Source:** 8-agent parallel audit swarm (security, concurrency, silent-failure, test-coverage, architecture, supply-chain, WA-parity, agent-sim+testing)
- **Companion:** `research.md` (approaches + sequencing)

## Severity summary

| Category | CRIT | HIGH | MED | LOW | Total |
|---|---|---|---|---|---|
| Security (SEC) | 0 | 2 | 4 | 3 | 9 |
| Concurrency (CON) | 0 | 2 | 4 | 3 | 9 |
| Silent-failure (SF) | 0 | 3 | 7 | 4 | 14 |
| Test-coverage (TEST) | 0 | 7 | 7 | 3 | 17 |
| Architecture (ARCH) | 1 | 3 | 1 | 0 | 5 |
| Release/supply-chain (REL) | 0 | 5 | 4 | 5 | 14 |
| **Total** | **1** | **22** | **27** | **18** | **68** |

Plus: 20 P0 parity features unimplemented (revoke, edit, block/unblock, group admin CRUD, disappearing-timer, groups-privacy, logout-all, view-once receive) — tracked separately under `specs/018-parity-hardening/features.md`. Inbound presence/typing/mentions + agent-self-throttle RPC + OCR/document ports listed as P0 agent-simulation gaps (see §Agent-sim).

---

## Master findings table

### Security (9)

| ID | SEV | Location | Summary |
|---|---|---|---|
| SEC-01 | ~~HIGH~~ **FIXED (pre-existing)** | `internal/adapters/primary/socket/listener.go` Check 5b | Parent dir is self-healed to 0700 and re-verified after chmod — the originally-flagged acceptance of looser modes no longer exists. Confirmed during PR #234 review of the live code. |
| SEC-02 | ~~HIGH~~ **FIXED** | `internal/app/subscriber_events.go` (PR #227) | Message/edit payloads now cross the bridge only as subscriber DTOs with all attacker text folded into the FR-005a `<channel>` envelope at translateDomainEvent — the single app-layer choke point. Raw fields are absent from the wire types. |
| SEC-03 | ~~MED~~ **FIXED (PR #234)** | `cmd/wa/cmd_pair.go`, `whatsmeow/pair_html.go` | Both writers now open with `O_CREATE\|O_EXCL\|O_NOFOLLOW` 0600 (stale file removed once and retried) — pre-planted symlinks are replaced, never followed. Remaining (UX, not security): per-profile filename; resolver.PairHTMLPath (FR-014) exists but is unwired. |
| SEC-04 | ~~MED~~ **FIXED (PR #234)** | `internal/app/method_send.go` auditErrDetail | Audit rows record only the typed error class (`code=<n>` / `internal`); raw upstream strings stay in the rotatable daemon log. All five recordAudit error sites converted. Log rotation itself remains open (operational). |
| SEC-05 | ~~MED~~ **FIXED (PR #244)** | `internal/adapters/secondary/whatsmeow/panic.go` | Every artefact removal is now `Lstat`-guarded (symlink ⇒ unlink the alias + report wipe-integrity error, never follow); `PanicArtefacts.MediaCacheRoot` added and wired from main.go — panic wipes the media cache tree, with a symlinked-root refusal. |
| SEC-06 | ~~MED~~ **FIXED (PR #234)** | `internal/app/group_admin.go` | `group.create` and `group.addParticipants` now run every participant through checkSafetyAndAudit with ActionGroupAdd (default-deny + rate budget + audit on refusal); adapter unreachable on denial, pinned by TestGroupOpsEnforceAllowlist. |
| SEC-07 | ~~LOW~~ **FIXED (PR #244)** | `cmd/wad/migrate.go` | `refuseSymlink` (Lstat gate) guards every migration move source: renamePlan, finishPartialRenames, ApplyRollback's reverse-copy (which opens through symlinks), and rollbackRenames (warn+skip). Pinned by TestMigration_RefusesSymlinkSource. |
| SEC-08 | ~~LOW~~ **FIXED (PR #244)** | `internal/adapters/secondary/execguard` | New `execguard.Verify` (regular file, no group/world write, owner ∈ {root, euid}; follows symlinks to the execve target) runs at construction/Detect in whispercpp, hear (darwin), and NLE (darwin). Construction-time by design — per-exec re-stat adds cost, not safety. |
| SEC-09 | ~~LOW~~ **FIXED (PR #244)** | `internal/adapters/secondary/transcribe/groq.go` | Multipart filename anonymised to `audio<ext>` (default `.ogg`) — extension kept as the Whisper format hint; `$HOME`/profile/content-hash never reach `api.groq.com`. |

### Concurrency + lifecycle (9)

| ID | SEV | Location | Summary |
|---|---|---|---|
| CON-01 | ~~HIGH~~ **FIXED (PR #240)** | `cmd/wad/main.go` | `KnownRecipientFunc` now queries through a daemon-lifetime ctx that `startupCleanup` cancels FIRST on both teardown paths — in-flight queries unwind before `historyStore.Close()`. |
| CON-02 | ~~HIGH~~ **FIXED (PR #136)** | `internal/adapters/secondary/whatsmeow/adapter.go` | Stale row: `panicWg` (added in PR #136) joins the LoggedOut Panic goroutine before `Close()` touches the containers, and `Panic` nils `a.history`/`a.session` after its own close, so the nil-guards skip the double-close. `context.Background()` is intentional — the R-07 wipe must complete even mid-shutdown. |
| CON-03 | ~~MED~~ **FIXED (PR #241)** | `internal/adapters/primary/socket/server.go`, `connection.go` | fanOutEvent + sendShutdownNotifications now snapshot frames under `c.mu` and push outside it; the backpressure raw-socket write is bounded by a 2s SetWriteDeadline so one wedged peer cannot stall the shared fan-out goroutine. Pinned by fanout_test.go. |
| CON-04 | ~~MED~~ **FIXED (PR #241)** | `internal/adapters/secondary/whatsmeow/adapter.go` | seq counter moved to an Adapter field (`historyReqCounter atomic.Uint64`) — parallel tests get independent sequences; no cross-test pending-entry contamination. |
| CON-05 | ~~MED~~ **FIXED (PR #241)** | `internal/app/schedule_runner.go` | Start derives a runner-owned cancellable ctx; Stop cancels it, stops timers, and fireWg-joins in-flight Fire callbacks (Add gated by `stopped` under mu — no Add/Wait race); arm-after-Stop is a no-op. Pinned by TestStopWaitsForInflightFire / TestStopCancelsFireCtx / TestScheduleAfterStopIsNoop. |
| CON-06 | ~~MED~~ **FIXED (PR #240)** | `cmd/wad/main.go`, `cmd/wad/cleanup.go` | Retention goroutine now runs on the daemon-lifetime ctx and closes a `retentionDone` channel; `waitRetention` joins it (bounded by `shutdownTimeout`) before the dispatch chain closes the history store. |
| CON-07 | ~~LOW~~ **STALE (already wired)** | `cmd/wad/runtime.go`, `internal/observability/crash.go:69` | Row predates spec 016 M-023/T077: `initRuntime` calls `observability` crash setup which wires `debug.SetCrashOutput` to the crashes/ dir; `wad crash list` reads it (FR-039). No action needed. |
| CON-08 | ~~LOW~~ **FIXED (PR #242)** | `internal/adapters/secondary/whatsmeow/backpressure.go`, `history_sync.go` | `emitStreamDrop` now pushes the drop record into the eventRing unconditionally (ring is local memory; channel send stays best-effort), so ResumeFrom replays the [from,to] hole instead of hiding it behind gap=false; hsync_ch_full audit re-kinded AuditPanic→AuditStreamDrop. Tests: `TestEmitStreamDropRingSurvivesFullChannel`, `TestDispatchHistorySyncFullChannelAuditKind`. |
| CON-09 | ~~LOW~~ **FIXED (PR #241)** | `internal/adapters/primary/socket/connection.go` | Writer goroutine now `defer c.cancel()` — every exit path (channel close included) tears the conn ctx down. Pinned by TestWriterExitCancelsConnCtx. |

### Silent-failure (14)

| ID | SEV | Location | Swallowed |
|---|---|---|---|
| SF-01 | ~~HIGH~~ **FIXED (PR #246)** | `cmd/wad/methods.go` | Allow add/remove now audit-before-persist with revert on failure — a grant/revoke without its durable audit row fails the RPC. Panic stays best-effort by design (R-07 recovery precedence, documented exemption). |
| SF-02 | ~~HIGH~~ **FIXED (PR #246)** | `cmd/wad/cleanup.go` | Startup-error teardown now logs every Close failure via `closeLogged` (success-path shutdown already did via `closeWithTimeout`). |
| SF-03 | ~~HIGH~~ **FIXED (PR #246)** | `cmd/wad/stores.go` | Best-effort store losses (webhooks/contacts/events) now surface as `degraded:[…]` in the `health` result via `DispatcherConfig.DegradedComponents`. |
| SF-04 | ~~MED~~ **FIXED (PR #257)** | `cmd/wad/migrate.go` | `renamePlan` removed the `.migrating` marker even when `rollbackRenames` failed to restore some moves — the "will retry on startup" log was a lie: next boot found no marker and no legacy session.db, stamped schema v2, and hid the half-migrated tree forever. Now `rollbackRenames` reports completeness and `abortRenames` keeps the marker on incomplete rollback, so startup recovery (`Recover` sees marker + destinations) drives the migration forward to completion. Symlink-skip (SEC-07) counts as incomplete. Tests pin marker-kept, marker-removed, and symlink-skip paths. |
| SF-05 | ~~MED~~ **FIXED (PR #258)** | `cmd/wad/allowlist.go` | Keep-last-good on parse error is the correct fail-safe; the gap was visibility — the watcher path left only a log line while `handleAdminReload`'s comment falsely claimed both triggers "converge on the identical atomic swap + audit emit". Now one shared `reloadAllowlist` backs fsnotify, SIGHUP, and admin.reload: success records `AuditReload`/granted, failure records `AuditReload`/refused (constant detail — no raw parse text per audit hygiene) so operators have a durable record that an edit did NOT take effect. Tests pin refused row + policy-kept, granted row + swap, and source attribution. |
| SF-06 | MED | `cmd/wad/service_darwin.go:157-161` | First `launchctl bootstrap` err lost during bootout-then-retry. |
| SF-07 | ~~MED~~ **FIXED (PR #259)** | `internal/app/embed_pipeline.go:fail` | Poison rows are now actively dropped: `IncrementAttempts` returns the post-increment count and `fail()` tombstones the row via `MarkIndexed` at `MaxAttempts`, bumping the operator-visible `Dropped()` counter + ERROR log. Port contract runner `RunPendingEmbeddingStoreContract` added (registry parity). |
| SF-08 | ~~MED~~ **FIXED (PR #260)** | `internal/app/eventbridge.go:Run` | Consecutive `stream.Next` errors now back off exponentially (100ms doubling to 5s ceiling, reset on success) and are surfaced: `StreamErrors()`/`LastStreamErrorUnix()` bridge accessors feed additive `streamErrors`/`lastStreamErrorTs` health fields. Forever-retry resilience preserved — the backoff paces it, never gives up. |
| SF-09 | ~~MED~~ **FIXED (PR #255)** | `internal/adapters/primary/socket/dispatch.go` | Row half-stale: recovery already logged `debug.Stack()` (landed with the earlier hardening pass). Real residue was correlation: client saw bare "Internal error" with no way for operator to find the matching log line. PR #255 extracts `dispatchRecovered` + adds crypto/rand `ref` — logged next to method/requestID/stack AND returned as `Internal error (ref <8hex>)`. Tests pin: -32603 code, ref format, ref present in log, panic value/stack never cross the wire. |
| SF-10 | ~~MED~~ **FIXED (PR #256)** | `cmd/wad/osroot.go:55-57` + `migrate.go` | Half stale: `statInDataRoot` already propagates non-NotExist errors (`return false, err`). Live half fixed: `readSchemaVersion` returned 1 for ALL read errors and the `autoMigrate` legacy-layout/marker stats treated EACCES/EIO as "no file" — an unreadable probe took the fresh-install branch, stamped schema v2, and permanently skipped migrating real 007 data. Now: `fs.ErrNotExist` keeps the defaults (missing → v1 / no marker / no legacy layout; garbage content still self-heals to v1), every other error class aborts autoMigrate with the failing step named. Tests pin EISDIR propagation + both EACCES abort paths. |
| SF-11 | LOW | `cmd/wad/migrate.go:576-588,649-660` | `_ = out.Close()` before return masks flush failure on written file. |
| SF-12 | LOW | `cmd/wad/migrate.go:760,810` | `defer db.Close()` — SQLite WAL checkpoint errors lost on read-mostly handles. |
| SF-13 | LOW | `internal/adapters/secondary/sqlitehistory/backup.go:130,158,183` | Backup source `db.Close()`/`in.Close()` error discarded; `VACUUM INTO` flush errors hidden. |
| SF-14 | LOW | `cmd/wad/allowlist.go` watchAllowlist defer | fsnotify `watcher.Close()` err discarded; leaks inotify slots on restarts. |

### Test-coverage (17)

| ID | SEV | Target | Gap |
|---|---|---|---|
| TEST-01 | ~~HIGH~~ **FIXED (PR #248)** | `MessageSender` | No `RunMessageSenderContract` — core send surface untested at port level. Resolution: MS1–MS6 exported as standalone `RunMessageSenderContract` + narrow `MessageSenderFactory`; `RunContractSuite` delegates (clauses unchanged, memory + whatsmeow still certify). |
| TEST-02 | ~~HIGH~~ **FIXED (PR #248)** | `EventStream` | `porttest/stream.go` exists but isn't a `Run…Contract` runner. Resolution: ES1–ES6 exported as `RunEventStreamContract` over `EventStreamHarness` (port + `EnqueueEvent` test hook); suite delegates. |
| TEST-03 | ~~HIGH~~ **FIXED (PR #248)** | `HistoryStore` | 009 landed without porttest; memory fake untested at port level. Resolution: HS1–HS6 exported as `RunHistoryStoreContract` over `HistoryStoreHarness`; suite delegates (memory + whatsmeow certify) AND `sqlitehistory.Store` now certifies directly (`historystore_contract_test.go`, deterministic timestamps via `InsertRaw`). |
| TEST-04 | ~~MED~~ **FIXED (PR #250)** | `ContactDirectory`, `GroupManager`, `SessionStore`, `Allowlist` | No shared Run…Contract runners. Resolution: clauses exported as `RunContactDirectoryContract`/`RunGroupManagerContract` (harness = port + `SeedContact`/`SeedGroup` hook), `RunSessionStoreContract` (bare port factory, no hooks), `RunAllowlistContract` (`AllowlistHarness` = port + `Grant`/`Revoke`; the old `grantOn`/`revokeOn` type-assertions fold into the suite delegate shim); `RunContractSuite` delegates, clauses unchanged. |
| TEST-05 | ~~HIGH~~ **STALE (already wired)** | 7× 018-Tier-2 ports | All 7 declared in `ports_018.go` with exported runners (`blocker.go`, `chat_state.go`, `message_moderator.go`, `poll_manager.go`, `privacy_settings.go`, `profile_editor.go`, `group_admin.go`); memory adapter certifies all 7 (`ports_018_test.go`), whatsmeow certifies privacy/profile/blocker/chatstate. |
| TEST-06 | ~~HIGH~~ **FIXED (PR #238)** | `method_tier2.go`, `method_markread.go`, `method_pair.go`, `method_status.go`, `method_thread.go`, `method_wait.go`, `idempotency_sweeper.go` | 7 dispatcher-level contract test files (+1148 lines): happy paths, not-wired port gates, param/JID validation, allowlist denials, audit decisions, limit clamps, waiter-registration race, synctest sweeper cadence. |
| TEST-07 | ~~MED~~ **FIXED (PR #251)** | `method_send.go` | Rate-limit + allowlist-deny branches spot-check missing. Mostly stale — T022/T023/T024 already pin ErrNotAllowlisted/ErrRateLimited/ErrWarmupActive and T022 asserts `denied:allowlist`. Real residue closed: T023/T024 now assert the `denied:rate`/`denied:warmup` audit decisions, and new `TestSendDeniedNotOnWhatsApp` covers the dispatcher-level `denied:not-on-whatsapp` path (gate fn alone was pinned before). `denied:unknown` stays uncovered by design — unreachable through the production `SafetyPipeline` error set. |
| TEST-08 | ~~LOW~~ **FIXED (PR #254)** | fuzz targets | Row was stale on count (tree already had 5: `FuzzParse`, `FuzzRateLimit`, `FuzzChannelWrap`, `FuzzTranslateEvent`, `FuzzDispatch`) but the two named gaps were real. PR #254 adds `FuzzCanonicalJSON` (internal/app: determinism, fixed-point, valid-JSON output, hash = sha256(canonical bytes), no input mutation; 920k execs clean) and `FuzzFrameRecv` (socket boundedChannel: frames never exceed cap or contain delimiter, oversized latch + exactly one -32004 peer frame, loop always terminates; 1.8M execs clean). |
| TEST-09 | ~~HIGH~~ **FIXED (PR #247)** | 8× `_test.go` in `internal/adapters/secondary/whatsmeow/` | Import `go.mau.fi/whatsmeow/...` without `//go:build integration` tag — violates v0 testing §6. Resolution: rule scoped, not files tagged — the adapter's own package tests run against in-package fakes offline (tagging them would drop that coverage from CI). Constitution v1.1.0 codifies the exemption; new depguard `tests-no-whatsmeow` enforces the rule for every other test file. |
| TEST-10 | ~~HIGH~~ **FIXED (PR #249)** | `cmd/wa/cmd_cli_surface_e2e_test.go` | Real gap was subcommand coverage, not the txtar file format: the package's established in-process e2e convention (fake JSON-RPC daemon on a unix socket + `runCmd`, asserting exact wire method/params — stronger than txtar stdout-matching) already covered 23 subcommands. PR #249 extends it to the 15 undriven ones: react, markRead, presence, history, search, thread, wait, session, allow, panic, health, sync, webhook, sendMedia, audit (19 tests: happy paths + usage-error guards + audit-verify filesystem paths). |
| TEST-11 | ~~HIGH~~ **STALE (already migrated)** | 25× `time.Sleep` in tests; worst in `schedule_runner_test.go` (5.1+15+10+7+3s) | Verified 11/06/2026: `schedule_runner_test.go` has zero literal sleeps and runs under `testing/synctest`; repo-wide literal `time.Sleep(` call sites = 6, exactly the agreed ceiling enforced by the `TestSynctestMigrationCount` drift guard (T3-20). |
| TEST-12 | MED | 10+ sites w/ un-injected `time.Now()` | `method_send/tier2/labels/schedule`, `ratelimiter`, `draft_sweeper` — breaks deterministic replay. |
| TEST-13 | LOW | SQLite test TempDir hygiene | Clean (0 offenders). No action. |
| TEST-14 | ~~MED~~ **STALE (already covered)** | `sockettest/hello_test.go` | Verified 11/06/2026: zero sleeps in the file, and `-32000 protocol_mismatch` is pinned three ways — wrong protocol version, non-hello first frame, and nothing-within-handshake-budget. |
| TEST-15 | MED | `IdempotencyStore` | Well-covered. No action. |
| TEST-16 | ~~MED~~ **STALE (already covered)** | bounded-send / stream.drop resume | Verified 11/06/2026: `whatsmeow/backpressure_test.go` runs under `testing/synctest` (channel form, no literal sleep); the drop+resume sequence is pinned by `socket/fanout_test.go` (stream.drop error frame first, `lastSeq` advances past the drop, NO advance on backpressure so the client resumes from last-delivered) + `heartbeat_test.go` `resumeSince` assertions; CON-08 stream-drop ring landed in PR #242. |
| TEST-17 | ~~MED~~ **FIXED (PR #253)** | panic-wipe | Socket-layer was already covered (`cmd_cli_surface_e2e_test.go` TestWaPanicEmitsRPC drives the full unix-socket round trip; adapter wipe semantics in `panic_close_test.go`). Real gap was the wad handler: `handlePanic`'s always-success contract (R-07) untestable against concrete `*wmAdapter.Adapter`. PR #253 narrows the param to a `panicWiper` interface + adds 3 handler tests: happy path (unlinked:true, wipe reason "rpc", durable AuditPanic/"wiped" row), wipe-error-still-succeeds, audit-failure-still-succeeds. |
| TEST-18 | ~~LOW~~ **STALE (verified 11/06/2026)** | `porttest/registry_test.go` | Policy already codified: registry.go header mandates same-commit growth ("every growth MUST touch this file in the same commit as the port declaration") + steps 1-3; registry_test.go guards sorted + no-dupes; 33 Run…Contract runners cover the ~26 registered ports. Same-commit invariant is process-level (not unit-testable); docs + parity coverage satisfy the row. |

### Architecture (5)

| ID | SEV | Location | Rule | Summary |
|---|---|---|---|---|
| ARCH-01 | ~~CRIT~~ MED (narrowed 11/06/2026) | `internal/app/ports_017.go` | rule 22 | Was: 7 Tier-2 ports with **zero use-case consumers**. 14 of 17 ports_017 ports now have dispatcher/composition-root consumers (`method_contacts/thread/search/idempotency/drafts/media/labels/schedule/embeddings`, `cmd/wad/main.go` wiring). Remaining orphans: `EventBuffer`, `EventBus`, `EventSubscription` — memory + sqliteevents adapters exist but no use case consumes them. Resolve by wiring or removing (design decision). |
| ARCH-02 | HIGH | `cmd/wa/cmd_pair.go:30-55` | CLAUDE.md §Repo layout | CLI writes HTML + spawns `open` — UI logic in thin-client binary. |
| ARCH-03 | ~~HIGH~~ **FIXED (PR #261)** | `internal/pairpath/pairpath.go` | rule 24 | Was: two copies of pair-HTML path constant with "keep in sync" comment (CLI + whatsmeow adapter) = silent drift; PathResolver carried a third, profile-suffixed variant with zero production callers, so FR-014 anti-collision was unwired. Now: single `pairpath.Path(profile)` leaf consumed by adapter (writer), CLI (reader), PathResolver — FR-014 profile suffix live end-to-end. |
| ARCH-04 | HIGH | `internal/adapters/primary/socket/server.go` (550), `internal/adapters/secondary/whatsmeow/adapter.go` (622) | Ousterhout deep-module | Files >500 lines absorbing multiple conversations. |
| ARCH-05 | ~~MED~~ **FIXED (PR #262)** | `internal/domain/schema.go` | rule 3 | Was: single-const `protoversion.go` with no types/methods. Not dead — `ProtoVersion` is consumed by both wire halves (CLI `rpc.go` sends, socket `hello.go` checks), so domain IS the right kernel; the file was the problem. Consolidated into `schema.go`, the established home for cross-binary version constants (`LayoutSchemaVersion` precedent), orphan file deleted, FR-012 freeze test kept as `schema_test.go`. |

### Release / supply-chain (14)

| ID | SEV | Location | Gap |
|---|---|---|---|
| REL-01 | HIGH | `release.yml:127` | syft emits CycloneDX default (1.5) not `cyclonedx-json@1.7=` per plan. |
| REL-02 | ~~HIGH~~ **FIXED** | `release.yml` | `cyclonedx-gomod app -licenses -std -json` invoked for cmd/wad + cmd/wa (verified 11/06/2026). |
| REL-03 | ~~HIGH~~ **STALE (fixed PR #170; verified 12/06/2026, PR #273)** | `.goreleaser.yaml:18,59` | Both build ids carry `flags: [-trimpath, -buildvcs=true, -buildmode=pie]` since ab745a6 — the row predates the fix and was never ticked. |
| REL-04 | ~~HIGH~~ **FIXED** | `release.yml` | Export SOURCE_DATE_EPOCH step runs before GoReleaser (verified 11/06/2026). |
| REL-05 | ~~HIGH~~ **STALE (ruleset live)** | repo settings | Verified 11/06/2026: `gh api repos/yolo-labz/wa/rulesets` returns 1 active ruleset (required green check, strict up-to-date, linear history, signed commits, no force-push, resolved review threads). |
| REL-06 | ~~MED~~ **FIXED** | all workflows | `step-security/harden-runner` present in all 13 workflows (verified 11/06/2026). |
| REL-07 | ~~MED~~ **STALE (verified 11/06/2026, PR #263)** | `release.yml` attest steps | Fixed before audit tick: all attest steps use `subject-checksums: dist/checksums.txt` (one attestation PER listed artifact), and the workflow comment documents the migration off the old manifest-only `subject-path`. Empirically proven: per-artifact `gh attestation verify` passed on the v2.1.0 asset set (17 artifacts). |
| REL-08 | ~~MED~~ **FIXED (PR #252)** | `lefthook.yml` | Half stale: the commit-msg `conventional` hook (`scripts/commit-msg-check.sh`) already enforces Conventional Commits + 72-char subject, and CI runs PR-title commitlint. Real residue closed: the script now also rejects commits missing a DCO `Signed-off-by:` trailer (`git commit -s`). |
| REL-09 | ~~MED~~ **FIXED (PR #263)** | CLAUDE.md §25 rollout | Was: §25 claimed "current state v0.3.3" + "new v0.4.0 tag signals the new bar" — `v0.4.0` was never tagged (history: v0.3.x → v1.x → v2.0.0-rc1 → v2.0.x → v2.1.0) and the Phase-1 rollout it promised has long shipped. Rewritten to v2.1.0 reality incl. automated Homebrew tap (hard GA gate) + Renovate-never-ran caveat. |
| REL-10 | ~~LOW~~ **STALE (verified 11/06/2026, PR #263)** | `README.md`, `SECURITY.md` | Claim outdated: README §Install carries a step-by-step `gh attestation verify` quickstart (per-artifact verify incl. the ≤v2.0.4 checksums-only caveat) and SECURITY.md §Supply-chain posture documents the full asset/verify matrix + "Recommended verify-before-install" one-liner. Landed with the #235/#49 docs wave after the audit snapshot. |
| REL-11 | ~~LOW~~ **VERIFIED-WORSE (11/06/2026, PR #263)** | `renovate.json` | Original question ("does the Renovate github-actions manager cover workflow SHA pins?") is moot: config extends `config:recommended` with no managers disabled, so coverage would be automatic — but the bot has NEVER opened a PR on this repo (zero renovate-authored PRs; all dep bumps manual, e.g. #49/#77/#78/#156; whatsmeow pinned 75d stale). `renovate.json` is dead config until the Renovate GitHub App is installed. **[pending] Pedro: install/enable Renovate app for yolo-labz/wa.** |
| REL-12 | LOW | `codeql.yml:30` | Go uses `autobuild`; 2026 guidance is `manual` + explicit `go build ./...`. |
| REL-13 | LOW | `CHANGELOG.md` | Confirmed clean — no hand edits. |
| REL-14 | ~~LOW~~ **FIXED (PR #273)** | CLAUDE.md | Governance-toolchain table row updated: `govulncheck in CI` → `OSV-Scanner V2 in CI (invokes govulncheck internally)`. Rule 25 + the research §"Drop standalone govulncheck" note already documented the replacement; the table was the last stale mention. Historical OPEN-Q7 row left as a period record. |

### Agent-simulation gaps (non-parity)

Tracked in `research.md` §Agent-sim fixes. Headline P0:

- No inbound `PresenceEvent`/`ChatPresenceEvent` → agent can't see typing.
- No `ratelimit.status` RPC → agent can't self-throttle.
- `Message.Mentions []JID` never parsed.
- Media ingestion audio-only; no `ImageOCR`/`DocumentText`/`VideoSummary` ports.
- Idempotent replay invisible; no `replayed: true` in `send` result.

### Parity gaps (feature-level)

Authoritative source: `specs/018-parity-hardening/features.md` (111 rows, dated 21/04/2026). Top P0 scheduled into Tier 2 as FR-014…FR-032:

- F-007 revoke-for-everyone · F-008 edit-sent · F-012 disappearing timer per-chat · F-014 view-once receive-respect · F-035/036 block/unblock · F-052-056 group CRUD + admin · F-080 groups-who-can-add-me privacy · F-086 logout-all (full wipe).

2026 additions NOT in dossier: usernames (upstream-blocked in whatsmeow), Meta-AI-in-chat (out-of-scope for agent-as-user), AI-suggested-replies (out-of-scope), HD-media toggle (no new API), Promoted Channels ads (P3 receive-side), passkeys (registration-time), chat-lock (client-UI only).

---

## Cross-reference index

Issues by file (top hot-spots):

- `cmd/wad/main.go` — SF-02, SF-03, CON-01, CON-06, CON-07
- `cmd/wad/methods.go` — SF-01
- `cmd/wad/migrate.go` — SEC-07, SF-04, SF-10, SF-11, SF-12
- `cmd/wa/cmd_pair.go` — SEC-03, ARCH-02, ARCH-03
- `internal/adapters/primary/socket/server.go` — SEC-02, CON-03, ARCH-04
- `internal/adapters/primary/socket/listener.go` — SEC-01
- `internal/adapters/primary/socket/dispatch.go` — SF-09
- `internal/adapters/secondary/whatsmeow/adapter.go` — CON-02, ARCH-04
- `internal/adapters/secondary/whatsmeow/history.go` — CON-04
- `internal/adapters/secondary/whatsmeow/panic.go` — SEC-05
- `internal/adapters/secondary/whatsmeow/backpressure.go` — CON-08
- `internal/adapters/secondary/{embed,transcribe}/*.go` — SEC-08, SEC-09
- `internal/app/ports_017.go` — ARCH-01
- `internal/app/eventbridge.go` — SF-08
- `internal/app/embed_pipeline.go` — SF-07
- `internal/app/method_*.go` — SEC-04, TEST-06, TEST-07, TEST-12
- `internal/app/schedule_runner.go` — CON-05
- `internal/domain/allowlist.go` — SEC-06
- `internal/domain/schema.go` — ARCH-05 (was `protoversion.go`, consolidated PR #262)
- `.github/workflows/release.yml` — REL-01, REL-02, REL-04, REL-07
- `.goreleaser.yaml` — ~~REL-03~~ (stale; fixed PR #170)

---

## Methodology

8 read-only audit agents ran in parallel (see `.specify/memory/constitution.md` §I and v0 testing contract). Each delivered ≤500-word findings with `path:line` cites. Totals reconciled against `specs/018-parity-hardening/features.md` (live 111-row dossier) and `specs/018-parity-hardening/testing-research.md` (018 testing blueprint). Bias toward breadth over fix-depth; `research.md` carries the sequencing + approach work.
