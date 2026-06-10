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
| SEC-01 | HIGH | `internal/adapters/primary/socket/listener.go:88` | Socket parent dir mode check accepts non-`0700`; violates CLAUDE.md §FS layout + FR-042; leaks presence via `ls`. |
| SEC-02 | ~~HIGH~~ **FIXED** | `internal/app/subscriber_events.go` (PR #227) | Message/edit payloads now cross the bridge only as subscriber DTOs with all attacker text folded into the FR-005a `<channel>` envelope at translateDomainEvent — the single app-layer choke point. Raw fields are absent from the wire types. |
| SEC-03 | MED | `cmd/wa/cmd_pair.go:24` | Pair HTML path missing profile segment + no `O_NOFOLLOW`/`O_EXCL`; symlink-planting TOCTOU in `/tmp`. |
| SEC-04 | MED | `internal/app/method_send.go:73,119,164`, `method_tier2.go:73`, `method_markread.go:47` | Raw error strings from whatsmeow logged to audit.log; upstream errors frequently embed body/recipient. audit.log never auto-rotates. |
| SEC-05 | MED | `internal/adapters/secondary/whatsmeow/panic.go:115-136` | `removePanicArtefacts` uses plain `os.Remove` (no symlink guard); media cache never nuked. |
| SEC-06 | MED | `internal/domain/allowlist.go` | `ActionGroupAdd`/`ActionGroupCreate` declared but never enforced at use-case sites; default-allow latent bug for incoming group ops. |
| SEC-07 | LOW | `cmd/wad/migrate.go:500,546,454` | Migration plan `os.Rename` without `lstat`/`O_NOFOLLOW`; symlink planting at pre-migration paths. |
| SEC-08 | LOW | `internal/adapters/secondary/{embed,transcribe}/*_darwin.go` + `whispercpp.go:89` | Resolved binary path never re-checked for group/world writability at exec time. |
| SEC-09 | LOW | `internal/adapters/secondary/transcribe/groq.go:102` | Absolute filesystem path leaks via multipart `filename=` to `api.groq.com`. |

### Concurrency + lifecycle (9)

| ID | SEV | Location | Summary |
|---|---|---|---|
| CON-01 | HIGH | `cmd/wad/main.go:326` | `KnownRecipientFunc` calls `QueryHistory(context.Background(), …)` per send — uncancellable on shutdown, races `historyStore.Close()`. |
| CON-02 | HIGH | `internal/adapters/secondary/whatsmeow/adapter.go:386-392` | `events.LoggedOut` fires `go Panic(context.Background(), …)` with no WaitGroup/cancel; races `sessionStore.Close()`. |
| CON-03 | MED | `internal/adapters/primary/socket/server.go:291-307, 351-435, 496-513` | `c.mu` held across `pushNotification(frame)` — one slow peer stalls every subscription scan + heartbeat tick. |
| CON-04 | MED | `internal/adapters/secondary/whatsmeow/history.go:95,152` | `historyReqSeqCounter` is `package var` — cross-test contamination; stale pending entry from prior test can eat later delivery. |
| CON-05 | MED | `internal/app/schedule_runner.go:141-146` | `time.AfterFunc` retains `r.ctx`; timers stopped but ctx not cancelled in `Stop()` → fire-after-Stop against closed schedule store. |
| CON-06 | MED | `cmd/wad/main.go:398-440` | Shutdown closes history/session stores (transitively via `waAdapter.Close()`) while `runRetentionCleanup` goroutine is mid-query. |
| CON-07 | LOW | `cmd/wad/main.go` (absent) | `runtime/debug.SetCrashOutput` never wired; 018 FR requirement unmet; panics in background goroutines lost. |
| CON-08 | LOW | `internal/adapters/secondary/whatsmeow/backpressure.go:108`, `history_sync.go:33` | `StreamDropEvent` itself subject to silent drop if event channel full → ring-seq ordering corruption; violates rule 12. |
| CON-09 | LOW | `internal/adapters/primary/socket/connection.go:83` | Writer goroutine never defers `c.cancel()` → leaks if jrpc2 handler panics and bypass cancel path. |

### Silent-failure (14)

| ID | SEV | Location | Swallowed |
|---|---|---|---|
| SF-01 | HIGH | `cmd/wad/methods.go:94-96,140-142,178,185-187` | `audit.Record` / `waAdapter.Panic` errors logged + discarded; allowlist mutations report success even when durable audit row never wrote. |
| SF-02 | HIGH | `cmd/wad/main.go:103,115-172,182-184` | Shutdown cascade `_ = store.Close()` on session/history/draft/schedule/audit — WAL checkpoint / audit flush failures silently lost. |
| SF-03 | HIGH | `cmd/wad/main.go:~145` | `contactsStore`/`eventsStore` open-fail warned-then-set-to-nil; handlers degrade silently with no readiness signal. |
| SF-04 | MED | `cmd/wad/migrate.go:546-549` | `rollbackRenames` retry-on-next-boot comment but marker file may be gone → stuck state hidden. |
| SF-05 | MED | `cmd/wad/allowlist.go:reload` | `loadAllowlist` parse error keeps previous in-memory policy; operator sees old policy enforced after edit. |
| SF-06 | MED | `cmd/wad/service_darwin.go:157-161` | First `launchctl bootstrap` err lost during bootout-then-retry. |
| SF-07 | MED | `internal/app/embed_pipeline.go:process` | Poison-drop past `MaxAttempts` logs only; no operator event, no audit row, no counter. |
| SF-08 | MED | `internal/app/eventbridge.go:80-87` | Upstream `stream.Next` errors retry forever with 100ms backoff; no cap, no surfaced subscribe-stream signal. |
| SF-09 | MED | `internal/adapters/primary/socket/dispatch.go:57-66` | Panic recovery without `debug.Stack()` or correlation-id; RPC returns generic "Internal error". |
| SF-10 | MED | `cmd/wad/osroot.go:55-57` + `migrate.go:readSchemaVersion` | `os.IsNotExist` branch returns default for ALL errors including EACCES/EIO. |
| SF-11 | LOW | `cmd/wad/migrate.go:576-588,649-660` | `_ = out.Close()` before return masks flush failure on written file. |
| SF-12 | LOW | `cmd/wad/migrate.go:760,810` | `defer db.Close()` — SQLite WAL checkpoint errors lost on read-mostly handles. |
| SF-13 | LOW | `internal/adapters/secondary/sqlitehistory/backup.go:130,158,183` | Backup source `db.Close()`/`in.Close()` error discarded; `VACUUM INTO` flush errors hidden. |
| SF-14 | LOW | `cmd/wad/allowlist.go` watchAllowlist defer | fsnotify `watcher.Close()` err discarded; leaks inotify slots on restarts. |

### Test-coverage (17)

| ID | SEV | Target | Gap |
|---|---|---|---|
| TEST-01 | HIGH | `MessageSender` | No `RunMessageSenderContract` — core send surface untested at port level. |
| TEST-02 | HIGH | `EventStream` | `porttest/stream.go` exists but isn't a `Run…Contract` runner. |
| TEST-03 | HIGH | `HistoryStore` | 009 landed without porttest; memory fake untested at port level. |
| TEST-04 | MED | `ContactDirectory`, `GroupManager`, `SessionStore`, `Allowlist` | No shared Run…Contract runners. |
| TEST-05 | HIGH | 7× 018-Tier-2 ports | `MessageModerator`/`ChatStateManager`/`Blocker`/`PrivacySettings`/`ProfileEditor`/`GroupAdmin`/`PollManager` — not declared in `ports_018.go`, zero tests. |
| TEST-06 | HIGH | `method_tier2.go`, `method_markread.go`, `method_pair.go`, `method_status.go`, `method_thread.go`, `method_wait.go`, `idempotency_sweeper.go` | Zero test lines for use cases with 2-8 error sites each. |
| TEST-07 | MED | `method_send.go` | Rate-limit + allowlist-deny branches spot-check missing. |
| TEST-08 | LOW | fuzz targets | Only `FuzzParse` (JID). Missing: `FuzzFrame` (JSON-RPC), `FuzzCanonicalJSON`. |
| TEST-09 | HIGH | 8× `_test.go` in `internal/adapters/secondary/whatsmeow/` | Import `go.mau.fi/whatsmeow/...` without `//go:build integration` tag — violates v0 testing §6. |
| TEST-10 | HIGH | `cmd/wa/testdata/script/*.txtar` | ZERO testscript files; 11 subcommands without e2e coverage. |
| TEST-11 | HIGH | 25× `time.Sleep` in tests; worst in `schedule_runner_test.go` (5.1+15+10+7+3s) | Real-clock waits; 4 non-synctest schedule tests. |
| TEST-12 | MED | 10+ sites w/ un-injected `time.Now()` | `method_send/tier2/labels/schedule`, `ratelimiter`, `draft_sweeper` — breaks deterministic replay. |
| TEST-13 | LOW | SQLite test TempDir hygiene | Clean (0 offenders). No action. |
| TEST-14 | MED | `sockettest/hello_test.go` | Real-sleep polling instead of synctest; `-32000 protocol_mismatch` path untested. |
| TEST-15 | MED | `IdempotencyStore` | Well-covered. No action. |
| TEST-16 | MED | bounded-send / stream.drop resume | `backpressure_test.go` uses real sleep; drop+resume sequence untested. |
| TEST-17 | MED | panic-wipe | App-layer + socket-layer integration missing; only adapter-level coverage. |
| TEST-18 | LOW | `porttest/registry_test.go` | Registry entries must land with new Run…Contract runners in same commit. |

### Architecture (5)

| ID | SEV | Location | Rule | Summary |
|---|---|---|---|---|
| ARCH-01 | CRIT | `internal/app/ports_017.go` | rule 22 | 7 Tier-2 ports declared with **zero use-case consumers** (Cockburn completeness violation). |
| ARCH-02 | HIGH | `cmd/wa/cmd_pair.go:30-55` | CLAUDE.md §Repo layout | CLI writes HTML + spawns `open` — UI logic in thin-client binary. |
| ARCH-03 | HIGH | `cmd/wa/cmd_pair.go:22-26` ↔ `internal/adapters/secondary/whatsmeow/pair_html.go` | rule 24 | Two copies of pair-HTML path constant with "keep in sync" comment = silent drift. |
| ARCH-04 | HIGH | `internal/adapters/primary/socket/server.go` (550), `internal/adapters/secondary/whatsmeow/adapter.go` (622) | Ousterhout deep-module | Files >500 lines absorbing multiple conversations. |
| ARCH-05 | MED | `internal/domain/protoversion.go` | rule 3 | No types, no methods — misplaced constants or dead file. |

### Release / supply-chain (14)

| ID | SEV | Location | Gap |
|---|---|---|---|
| REL-01 | HIGH | `release.yml:127` | syft emits CycloneDX default (1.5) not `cyclonedx-json@1.7=` per plan. |
| REL-02 | HIGH | `release.yml:84-87` | `cyclonedx-gomod` installed but never invoked. |
| REL-03 | HIGH | `.goreleaser.yaml:18,59` | `-buildmode=pie` absent. |
| REL-04 | HIGH | `release.yml` goreleaser step | `SOURCE_DATE_EPOCH` not exported before tag release — non-reproducible. |
| REL-05 | HIGH | repo settings | Still on classic branch protection; `gh api …/rulesets` returns `[]`. |
| REL-06 | MED | `ci.yml`, `codeql.yml`, `osv-scan.yml`, `scorecard.yml`, `reproducibility.yml` | `step-security/harden-runner` only in `release.yml`. |
| REL-07 | MED | `release.yml:130-144` | `attest-build-provenance subject-path: dist/checksums.txt` attests checksum only, not binaries. |
| REL-08 | MED | `lefthook.yml` | No commitlint hook + no DCO sign-off enforcement. |
| REL-09 | MED | CLAUDE.md §25 rollout | `v0.4.0` tag claim drift — repo skipped to v1.x/v2.0.0-rc1. |
| REL-10 | LOW | `README.md`, `SECURITY.md` | No `gh attestation verify` quickstart. |
| REL-11 | LOW | `release.yml:105` | GoReleaser SHA bumps; confirm Renovate manager includes. |
| REL-12 | LOW | `codeql.yml:30` | Go uses `autobuild`; 2026 guidance is `manual` + explicit `go build ./...`. |
| REL-13 | LOW | `CHANGELOG.md` | Confirmed clean — no hand edits. |
| REL-14 | LOW | CLAUDE.md | Stale "govulncheck in CI" bullet; OSV-Scanner V2 subsumes. |

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
- `internal/domain/protoversion.go` — ARCH-05
- `.github/workflows/release.yml` — REL-01, REL-02, REL-04, REL-07
- `.goreleaser.yaml` — REL-03

---

## Methodology

8 read-only audit agents ran in parallel (see `.specify/memory/constitution.md` §I and v0 testing contract). Each delivered ≤500-word findings with `path:line` cites. Totals reconciled against `specs/018-parity-hardening/features.md` (live 111-row dossier) and `specs/018-parity-hardening/testing-research.md` (018 testing blueprint). Bias toward breadth over fix-depth; `research.md` carries the sequencing + approach work.
