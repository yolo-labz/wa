# research.md — approaches, features, and testing plan

- **Compiled:** 22/04/2026
- **Ref commit:** 2543d78 (v2.0.0-rc1)
- **Companion:** `issues.md` (68 issues + parity + agent-sim)
- **Downstream use:** feed into `/speckit:specify` for the follow-up feature bundle (working title: 019-real-user-parity). Binds to CLAUDE.md rules 1–25 + constitution.

This document pairs each finding class in `issues.md` with a chosen approach, at least one rejected alternative (CLAUDE.md rule 20), and the first file to touch. It then enumerates the P0 feature-parity + agent-simulation gaps and closes with the real-testing strategy.

---

## Part 1 — Issue remediation approaches

### Security

**Chosen:** treat SEC-01/02/05/07 as a "filesystem-trust hardening" sub-feature; SEC-03 as a single pair-HTML refactor (pairs with ARCH-02/03 consolidation); SEC-04 as a dedicated "audit log redaction" pass; SEC-06 as an allowlist wiring audit; SEC-08/09 as a minor exec-hygiene follow-up.

| Finding | Approach | Rejected alternative | First file |
|---|---|---|---|
| SEC-01 | Tighten `validateParentDir` mode check to `== 0o700` exact. | Mask `& 0o077 != 0` — accepts `0o700`, `0o500`, etc., still world-safe but looser than spec says. | `internal/adapters/primary/socket/listener.go` |
| SEC-02 | Introduce `ChannelWrap(body string, src, chat, sender JID, ts Time) string` in `internal/domain/channelwrap.go`; call from `cmd/wad/adapter.go` before populating `socket.Event.Body`. | Wrap in `socket/server.go` at marshal time — bad layering, primary adapter would carry domain-policy knowledge. | `internal/domain/channelwrap.go` (new) + `cmd/wad/adapter.go` |
| SEC-03 | Per-profile path + `O_CREATE\|O_WRONLY\|O_TRUNC\|O_EXCL\|syscall.O_NOFOLLOW` + unlink-first-on-EEXIST if `euid`-owned. | Keep `/tmp` but add a profile-scoped subdir — introduces dir-creation race, more moving parts. | `internal/adapters/secondary/whatsmeow/pair_html.go` (consolidate) |
| SEC-04 | Introduce `classifyErr(err) string` returning a bounded set (network/auth/ratelimit/protocol/badrequest/server); audit.log gets the class, slog.Debug gets raw. | Drop raw error entirely — operators lose debuggability. | `internal/app/errclass.go` (new) |
| SEC-05 | `os.Lstat` + reject symlink before every `os.Remove`; extend `PanicArtefacts` with `MediaCacheRoot` — panic MUST wipe media cache for the profile (spec amendment). | Add `unix.AT_SYMLINK_NOFOLLOW` via `unlinkat` Linux-only — asymmetric, Darwin needs the lstat path anyway. | `internal/adapters/secondary/whatsmeow/panic.go` |
| SEC-06 | Add porttest fixture `AssertAllowlistActionChecked(t, action, handlerFn)` used by every use case that delegates to an adapter with admin-facing verbs; codegen the coverage list from `internal/app/dispatcher.go`. | Manual review — rule 14 says negations are prohibitions; LLMs under-weight. Automate. | `internal/app/porttest/allowlist_enforcement.go` (new) |
| SEC-07 | `os.Lstat` gate + reuse the `socket/symlink_unix.go` `openat2(RESOLVE_NO_SYMLINKS)` pattern for migration plan. | `filepath.EvalSymlinks` + comparison — TOCTOU race, no atomicity. | `cmd/wad/migrate.go` |
| SEC-08 | At adapter construction, stat resolved binary; reject if `mode & 0o022 != 0` or `owner != root && != euid`. | Runtime check per-exec — 10x cost for no safety gain over construction check. | `internal/adapters/secondary/{embed,transcribe}/*.go` |
| SEC-09 | Replace `CreateFormFile(path)` with stable basename `audio.ogg` or `sha256(content)[:12].ogg`. | Strip to `filepath.Base(path)` — still leaks profile name + content hash. | `internal/adapters/secondary/transcribe/groq.go` |

### Concurrency + lifecycle

**Chosen:** adopt a daemon-wide "shutdown ledger" pattern — every background goroutine registers a `WaitGroup`; `main.go` drains all ledgers before closing any store. Combine with `SetCrashOutput`.

| Finding | Approach | Rejected | First file |
|---|---|---|---|
| CON-01 | **SUPERSEDED by Feature 113:** the daily new-recipient policy and `KnownRecipientFunc` closure are removed. | Keeping a history lookup for deleted policy would preserve dead lifecycle coupling. | `specs/113-no-daily-send-caps/constitution-amendment.md` |
| CON-02 | Add `a.panicWg` on `Adapter`; `Panic` goroutine uses `.Add(1)/defer Done()`; `Close()` awaits it. | Synchronous Panic call from event handler — whatsmeow rejects blocking handlers. | `internal/adapters/secondary/whatsmeow/adapter.go` |
| CON-03 | Release `c.mu`, collect frames into local slice, push after unlock. | Per-connection writer goroutine + unbounded chan — backpressure disappears. | `internal/adapters/primary/socket/server.go` |
| CON-04 | Move `historyReqSeqCounter` onto `Adapter` as `atomic.Uint64`. | Reset package var in test teardown — fragile, order-dependent. | `internal/adapters/secondary/whatsmeow/history.go` |
| CON-05 | Add `r.runCtx`+`cancel` on `scheduleRunner`; cancel in `Stop()`; `AfterFunc` callback reads `r.runCtx`. | `sync.Once`-gated nop after Stop — still queues work on closed DB. | `internal/app/schedule_runner.go` |
| CON-06 | Add `retentionWg`; drain before `waAdapter.Close()`. | Run retention only at boot — loses the "continuous cleanup" invariant from 009. | `cmd/wad/main.go` |
| CON-07 | Early in `main()`, open `$XDG_STATE_HOME/wa/<p>/crash.log`, `debug.SetCrashOutput(f, debug.CrashOptions{})`. | Keep stderr-only — launchd pipes stderr to plist path already, but SetCrashOutput is the 018 FR. | `cmd/wad/main.go` |
| CON-08 | Dropped-drop counter on `Adapter`; surface via `ratelimit.status` RPC (reuse the 018 agent-sim work). | Block on drop — violates rule 12 (silent fallback vs blocking tradeoff: neither; count+report). | `internal/adapters/secondary/whatsmeow/backpressure.go` |
| CON-09 | `defer c.cancel()` inside `startWriter` goroutine exit. | Only cancel on write err — misses handler-panic path. | `internal/adapters/primary/socket/connection.go` |

### Silent-failure

**Chosen:** define `closeOrWarn(log, closer)` + `closeOrErr(closer) error` helpers in `internal/pkg/io/closing.go` (new). Convert every `defer _ = x.Close()` site. Per-RPC audit-row failures become `-32xxx` error codes. Store-degraded events become first-class notifications.

| Finding class | Approach |
|---|---|
| Shutdown close-err drop (SF-02, SF-11, SF-12, SF-13, SF-14) | `closeOrErr` helper; `main.go` Run returns first error. |
| Audit-row write fails (SF-01) | New JSON-RPC error code `-32170 AuditWriteFailed`; audit.Record returns typed err; handler decides: still-mutate (log) vs refuse (return RPC err). For allowlist ops → refuse. For send ops → mutate + log (matches CLAUDE.md audit-log philosophy: append-only, never lose user work). |
| Degraded-store silent (SF-03, SF-05) | New notification `state.degraded` with `component, reason, since`; `status` RPC includes `degraded[]`. |
| Stuck rollback / retry-forever (SF-04, SF-08) | Hard-fail on N consecutive errors; surface via `state.event_stream_failed` / stuck marker. |
| Dropped error in retry (SF-06) | `errors.Join(firstErr, retryErr)`. |
| Panic recovery (SF-09) | Wrap in middleware: capture `debug.Stack()`, generate 8-hex correlation id, log structured, include id in JSON-RPC error data. |
| os.IsNotExist misuse (SF-10) | `errors.Is(err, fs.ErrNotExist)` only; everything else bubbles. |
| Poison-drop visibility (SF-07) | Emit `state.embedding_dropped` notification + audit event `embed.dropped`. |

### Test coverage

**Chosen:** one feature-sized cleanup pass "019-real-user-parity / testing-sub-feature" that lands all 17 items together — contract runners, testscript scaffolding, synctest migration, fuzz expansion. Rationale: CLAUDE.md rule 6 caps `tasks.md` ≤ 25 — this fits.

Ordering inside that pass:

1. Declare all missing ports (7 Tier-2 + any 019 additions) before writing runners (avoid cascading recompiles).
2. Write `Run…Contract` runners with the **memory fake** first (fast), then flip each whatsmeow test to run against the same contract + add `//go:build integration` tag.
3. Scaffold `cmd/wa/testscript_test.go` via `testscript.Main`; add 14 `.txtar` files (list in §Testing below).
4. Migrate 25 `time.Sleep` sites to `testing/synctest` (Go 1.25 GA). Pattern: `synctest.Test(t, func(t *testing.T){ … synctest.Wait() })`.
5. Add 6 fuzz targets + seed corpora.

**Rejected:** land each per feature — violates rule 6 (one cleanup spans multiple features; better as a single spec).

### Architecture

| Finding | Approach | First file |
|---|---|---|
| ARCH-01 | Tier 2 PR (branch `032-018-tier2-ports`) declares the 7 ports AND their use cases in the same commit — not in separate commits (rule 22 co-landing). | `internal/app/ports_018.go` (new, existing stub) |
| ARCH-02 | Strip `--browser` from `cmd/wa/cmd_pair.go`; move browser-launch into `cmd/wad` as an optional composed adapter `internal/adapters/primary/pairbrowser/`. Keep CLI to RPC call + write QR text to stdout. | `cmd/wa/cmd_pair.go` |
| ARCH-03 | Lift `PairHTMLPath(profile)` into `internal/domain/pairhtml_path.go`; delete duplicate. | `internal/domain/pairhtml_path.go` (new) |
| ARCH-04 | Split `socket/server.go` into `server.go` (wire), `lifecycle.go`, `fanout.go`. Split `whatsmeow/adapter.go` — already has sibling files; move flag/audit stitching out. | `internal/adapters/primary/socket/fanout.go` (new) |
| ARCH-05 | Either fold `protoversion.go` constants into `internal/domain/handshake.go` or delete. | — |

**Rejected:** "ports-and-consumers in separate commits for review ease" — drifts, invites dead ports in main (018-ceremony debt).

### Release / supply chain

Phase-1 rollout continuation:

| Finding | Approach | First file |
|---|---|---|
| REL-01 | `syft . -o cyclonedx-json@1.7=dist/sbom.cdx.json -o spdx-json=dist/sbom.spdx.json`. | `.github/workflows/release.yml` |
| REL-02 | Add `cyclonedx-gomod app -licenses -std -json -output dist/sbom.gomod.cdx.json ./cmd/wad`. | `.github/workflows/release.yml` |
| REL-03 | Add `-buildmode=pie` to both build flags in `.goreleaser.yaml`. | `.goreleaser.yaml` |
| REL-04 | `export SOURCE_DATE_EPOCH=$(git log -1 --format=%ct)` before `goreleaser release --clean`. | `.github/workflows/release.yml` |
| REL-05 | Bootstrap Ruleset via `gh api`: `enforcement: disabled` → activate → delete classic protection (two-step switchover avoids lockout). | `.github/rulesets/main.json` (new) |
| REL-06 | Add `step-security/harden-runner` at `audit` to every workflow's first step. | all `.github/workflows/*.yml` |
| REL-07 | `subject-path: 'dist/*.tar.gz'` (globs binaries per attest docs). | `.github/workflows/release.yml` |
| REL-08 | Add lefthook `commit-msg` running `@commitlint/config-conventional` + DCO check via `git interpret-trailers`. | `lefthook.yml` |
| REL-09 | Update CLAUDE.md §25 to reflect absorption into v1.x → v2.0.0-rc1 (rollout succeeded, bar just moved). | `CLAUDE.md` |
| REL-10 | README + SECURITY get `## Verify a release` with `gh attestation verify`. | `README.md`, `SECURITY.md` |
| REL-11 | Confirm Renovate manager includes `github-actions` for GoReleaser action. | `renovate.json` |
| REL-12 | Switch CodeQL Go matrix to `build-mode: manual` + `go build ./...`. | `.github/workflows/codeql.yml` |
| REL-14 | Strike `govulncheck in CI` from CLAUDE.md — OSV-Scanner V2 subsumes. | `CLAUDE.md` |

**Rejected:** wait-for-v2.0.0 tag to land SBOM fixes — then provenance covers wrong content under the rc1 tag and the release train sees divergent artifacts. Land supply-chain cleanup into `v2.0.0-rc2` before GA.

---

## Part 2 — Agent-as-user simulation gaps (P0 for the mission)

CLAUDE.md mission: "turns a personal WhatsApp account into an AI-mediated personal assistant." For the LLM to pass for a human, the following are required ON TOP OF the 20 P0 parity features (see §Part 3):

| # | Gap | Approach | Rejected | First file |
|---|---|---|---|---|
| AGE-01 | No `ratelimit.status` RPC → agent cannot self-throttle (hits `-32200` surprise). | If implemented, expose only the active per-second/per-minute windows and warmup; Feature 113 removes `perDay`, `perRecipientDaily`, and `newRecipientsToday`. | Expose budgets via `status` RPC only — coarse, doesn't surface refill windows. | `internal/app/method_ratestatus.go` (new) |
| AGE-02 | Inbound typing/presence not translated. | Add `PresenceEvent{Chat,From,State,TS}` + `ContactPresenceEvent`; translate `events.ChatPresence`/`events.Presence`; wire via `SubscribePresence`. | Skip presence entirely — removes huge human-mimicry signal. | `internal/domain/event.go` + `internal/adapters/secondary/whatsmeow/translate_event.go` |
| AGE-03 | No per-participant group receipts. | Extend `ReceiptEvent` with `From JID` (preserve recipient semantics via kind). | Separate `GroupReceiptEvent` type — schema bloat, same fields. | `internal/domain/event.go` |
| AGE-04 | `Message.Mentions` never parsed. | Extract `ContextInfo.MentionedJIDs`; add `Message.Mentions []JID`. | Regex `@<digits>` on body — misses bare @JID mentions, false-positives emails. | `internal/domain/message.go` + event translator |
| AGE-05 | Idempotent replay invisible. | Add `replayed bool` to `send` RPC result; `IdempotencyStore.LoadOrStore` returns `(cached, replayed)`. | Side-channel log — agent can't react programmatically. | `internal/app/ports_017.go` + `method_send.go` |
| AGE-06 | Media coverage audio-only. | Add `ImageOCR`, `DocumentText`, `VideoSummary` ports; Tier-3 sidecar adapters exec `tesseract` / `pdftotext` / `ffmpeg -vf select=eq(pict_type\,I)` + existing transcriber. | Single `MediaExtractor` omnibus port — violates CLAUDE.md rule 21 (one port = one conversation). | `internal/app/ports_019.go` (new) |
| AGE-07 | Disconnect event has no reason/next-attempt. | Add `Reason string` + `NextAttemptAt time.Time` to `ConnectionEvent`. | Leave to logs — agent can't UX it. | `internal/domain/event.go` |
| AGE-08 | Draft expiry invisible. | Add `ExpiresAt` to `draft.get` result. | — | `internal/app/method_draft.go` |
| AGE-09 | No schedule fire-feedback. | Emit `ScheduledFiredEvent` on success/failure. | Poll `schedule.list` — inefficient, missed-window risk. | `internal/app/schedule_runner.go` + `internal/domain/event.go` |

---

## Part 3 — Feature-parity P0 set

Authoritative: `specs/018-parity-hardening/features.md` (111 rows, 21/04/2026).

20 P0 items already scheduled as FR-014…FR-032 into Tier 2 (`tasks-tier2.md`) covering branch `032-018-tier2-ports`:

- **Messaging:** revoke-for-everyone (F-007), edit-sent (F-008), forward helper (F-009), disappearing timer per-chat (F-012), view-once receive-respect (F-014).
- **Chat ops:** archive/mute/pin/mark-unread (F-041..044), clear/delete chat (F-045..046).
- **Presence:** outbound recording-audio verify (F-032 fits under FR-032 outbound hygiene); inbound presence (see AGE-02).
- **Profile:** setPushName, setAbout (F-065, F-066).
- **Privacy:** getPrivacySettings, setPrivacySetting, groups-who-can-add-me, read-receipts toggle (F-078..081).
- **Blocking:** block/unblock/list (F-035..037, FR-018/019).
- **Groups:** create, add/remove participants, promote/demote, leave, edit name/desc/icon, invite-link, announce/locked/join-approval (F-052..062).
- **Account:** logout-all (FR-031 full wipe vs current R-07 panic).

**Not scheduled (upstream-blocked or policy-forbidden):**
- Usernames (whatsmeow has no binding as of 22/04/2026).
- Place voice/video call (whatsmeow is receive-only).
- Backup / change-number / delete-account / request-info (no whatsmeow helpers).
- Broadcast lists (forbidden by constitution §Safety).

Status updates, channels/newsletters, communities: P2/P3 — not MVP-blocking for agent-as-user, defer past v2.0.0.

---

## Part 4 — Real testing strategy

Augments (does not replace) `specs/018-parity-hardening/testing-research.md`. Everything below assumes the CLAUDE.md v0 contract still binds.

### 4.1 Property-based (pgregory.net/rapid) — 6 targets

| Invariant | File | Why |
|---|---|---|
| JID canonicalization roundtrip: `Parse(j.String()) == j` | `internal/domain/jid_property_test.go` | Core value object, 1 000s of parse sites. |
| Idempotency determinism + collision detection | `internal/app/idempotency_property_test.go` | 018 FR-034a correctness gate. |
| Rate-limiter monotonic refill under synctest clock | `internal/app/ratelimiter_property_test.go` | Warmup tier transitions notoriously off-by-one. |
| Allowlist monotonicity: adding never revokes | `internal/domain/allowlist_property_test.go` | Prevents accidental default-deny widening. |
| JSON-RPC decode/encode symmetry | `internal/adapters/primary/socket/canonicaljson_property_test.go` | Cross-version compat. |
| `StreamDropEvent` gap invariant: `ToSeq - FromSeq + 1 == DroppedCount` | `internal/adapters/secondary/whatsmeow/backpressure_property_test.go` | Subscribe-stream correctness. |

**Rejected:** `gopter` — stagnant since 2024, worse shrinker.

### 4.2 testing/synctest migration (Go 1.25 GA)

Priority list (from 25 `time.Sleep` sites):
1. `internal/app/schedule_runner_test.go` — 5.1+15+10+7+3s real waits.
2. `internal/app/idempotency_sweeper_synctest_test.go` (new).
3. `internal/app/draft_sweeper_test.go`.
4. `internal/app/ratelimiter_test.go` — finish partial migration.
5. `internal/adapters/primary/socket/heartbeat_test.go`.
6. `internal/adapters/primary/socket/hello_test.go` (018 handshake).
7. `internal/adapters/secondary/whatsmeow/backpressure_test.go`.

Pattern: `synctest.Test(t, func(t *testing.T){ … ; synctest.Wait() ; assert })`.

**Rejected:** generous-timeout real clock — flakes in self-hosted Dokku CI.

### 4.3 testscript `.txtar` coverage — 14 files

Scaffold at `cmd/wa/testscript_test.go` via `testscript.Main(m, map[string]func(){"wa": cmd.Execute, "wad": wadcmd.Execute})`.

Required golden scripts under `cmd/wa/testdata/script/`:

- `send_happy.txtar`, `send_ratelimit_refuse.txtar` (-32200), `send_allowlist_refuse.txtar` (-32100)
- `pair_qr.txtar`, `pair_phone.txtar`
- `subscribe_stream_drop.txtar`
- `draft_approve.txtar`
- `schedule_list.txtar`
- `contacts_lookup.txtar`
- `thread_get.txtar`
- `labels_create.txtar`
- `embeddings_status.txtar`
- `media_download.txtar`
- `panic.txtar`

**Rejected:** `bats` — not Go-native, breaks `go test ./...` coverage.

### 4.4 Fuzz corpus — 7 targets (Scorecard +10 already earned with FuzzParse)

| Target | File |
|---|---|
| `FuzzJID` — grow corpus | `internal/domain/jid_fuzz_test.go` (exists) |
| `FuzzAllowlistTOML` | `internal/domain/allowlist_fuzz_test.go` |
| `FuzzHandleRequest` | `internal/adapters/primary/socket/jsonrpc_fuzz_test.go` |
| `FuzzTranslateEvent` | `internal/adapters/secondary/whatsmeow/translate_event_fuzz_test.go` |
| `FuzzCanonicalJSON` | `internal/adapters/primary/socket/canonicaljson_fuzz_test.go` |
| `FuzzMessageBody` — 64KB/16MB truncation | `internal/domain/message_fuzz_test.go` |
| `FuzzPhoneParse` | `internal/domain/phone_fuzz_test.go` |

Nightly `.github/workflows/fuzz.yml` with `-fuzztime=600s`. Seeds committed under `testdata/fuzz/`.

**Rejected:** `go-fuzz` v1 — deprecated since Go 1.18 native fuzz.

### 4.5 Chaos injection — `internal/test/chaos/`

API: `chaos.Wrap(port, chaos.Spec{DisconnectEvery, RateLimit429Rate, LatencyJitter, StreamReplacedAtSeq, BufferOverflow})`. Wraps memory adapters. Predictable seeded faults.

**Rejected:** Toxiproxy — TCP-only; our IPC is unix socket.

### 4.6 Integration-without-WA harness

`cmd/wad/e2e_harness_test.go` — spawns real `wad` in-process with memory ports over `t.TempDir()/wa.sock`, drives real `cmd/wa` subcommands via `testscript` + `exec`. **Runs every PR** (no build tag). Complements 018-Tier-1 sockettest work.

One-file mock MCP channel client (`cmd/wad/e2e_channel_test.go`, ~100 LoC) asserts every inbound `event` notification carries a `<channel source="wa" chat=… sender=… ts=…>…</channel>` wrapper — hard contract against CLAUDE.md §Safety rule 5 / Constitution §V.29.

**Rejected:** full Docker compose — memory ports suffice; Compose adds 3 min per CI run.

### 4.7 Mutation testing (nightly only)

`gremlins.dev` scoped to `internal/{domain,app}/` (exclude adapters — IO-heavy, noisy). `.github/workflows/mutation.yml` wall-clock ~45 min. **Never a PR gate** — v0.x still churns.

**Rejected:** `avito-tech/go-mutesting` — less active.

### 4.8 Coverage floors per package

Enforced via `go tool cover -func | awk` in CI:

| Package tree | Floor | Rationale |
|---|---|---|
| `internal/domain/` | 90% | Pure, no IO — invariant regressions unacceptable. |
| `internal/app/` | 80% | Use cases — some error paths hard to reach. |
| `internal/adapters/secondary/memory/` | 85% | It's a fake; exhaustive is the point. |
| `internal/adapters/secondary/whatsmeow/` | 60% | Integration-gated; contract suite via fake covers the rest. |
| `internal/adapters/secondary/sqlite*/` | 75% | — |
| `cmd/{wa,wad}/` | 70% | — |

**Rejected:** single global floor — flattens incentives; domain and cmd have vastly different test-ability.

### 4.9 Prohibitions suite — `internal/test/prohibitions/`

CLAUDE.md rule 14: "negations are prohibitions, not examples." Codify:

- `TestNoCGO` — assert no `import "C"` anywhere.
- `TestDomainImportsZeroWhatsmeow` — runtime cross-check of depguard.
- `TestSocketRequires0700Parent0600Sock` — stat the socket.
- `TestAllowlistRefusalNeverCallsSend` — memory spy: on `-32100`, `MessageSender.Send` call count == 0.
- `TestRateLimitRefusalNeverCallsSend` — same for `-32200`.
- `TestStreamDropEmittedOnBufferOverflow` — never silent drop (rule 12).
- `TestNoForceFlag` — testscript: `wa profile rm --force` exits 64.
- `TestChannelEnvelopeWrapsUntrustedBody` — every inbound subscribe-stream `event` notification MUST contain `<channel source="wa"`.

**Rejected:** depguard alone — compile-time only; misses runtime silent-fallback regressions.

---

## Part 5 — Sequencing plan

Recommended roll-up into 3 feature branches (each ≤25 tasks per rule 6):

### Feature 019-real-user-parity (P0 agent-sim + critical-path safety)

Scope: AGE-01..AGE-09 + SEC-02 + CON-02 + CON-07 + SF-01 + SF-03 + SF-09 + ARCH-01 (co-land Tier-2 consumers). Plus the prohibitions suite and integration-without-WA harness.

Why bundled: the 7 Tier-2 ports exist as declarations but have no consumers (ARCH-01 critical). Landing the full Tier-2 stack fixes ARCH-01 AND gives the concrete methods the agent-sim fixes call. SEC-02 + SF-01 block safe launch.

Target: `v2.0.0-rc2`.

### Feature 020-supply-chain-phase-1-complete

Scope: REL-01..REL-12 (supply-chain gaps). Pure release-engineering. Lands same PR-train as 019 since they share the release workflow.

Target: rolled into `v2.0.0-rc2`.

### Feature 021-tests-and-hygiene

Scope: all TEST-* (contract runners, testscript, synctest, fuzz, chaos, coverage floors). Plus ARCH-02..ARCH-05 (pair HTML consolidation, file splits). Plus remaining low-sev SEC/SF/CON items.

Target: `v2.0.0` GA.

### Feature 022-media-extraction (optional, defer past GA)

Scope: AGE-06 `ImageOCR`/`DocumentText`/`VideoSummary` ports + Tier-3 sidecar adapters.

**Rejected sequencing:** one giant 70-issue spec — violates rule 6 (≤25 tasks). Three-feature split matches the velocity constraint and keeps each release auditable.

---

## Part 6 — Inputs for `/speckit:specify` (019-real-user-parity)

When the user runs `/speckit:specify` next, suggested one-liner feature description:

> "Harden the daemon for real agent-as-user simulation: wire the 7 declared but unused Tier-2 ports to concrete use cases (revoke, edit, archive/mute/pin, block/unblock, privacy, profile, group-admin, polls), close the P0 safety gaps (`<channel>` envelope on subscribe-stream events, audit-write error surfacing, `SetCrashOutput`, panic goroutine WaitGroup), add agent-observability RPCs (`ratelimit.status`, inbound presence/typing, replayed-bool on send result, per-participant group receipts, mentions parsing, disconnect reason, draft expiry, scheduled-fire events), and land the prohibitions test suite + integration-without-WA harness. Out of scope: media OCR (separate 022), communities/channels/status, usernames."

That spec will cite `issues.md` IDs (AGE-01..09, SEC-02, CON-02/07, SF-01/03/09, ARCH-01) for traceability — CLAUDE.md rule 11.

---

## References

- CLAUDE.md (24 binding rules + anti-patterns + §Safety/Daemon/FS/IPC).
- `.specify/memory/constitution.md` (principles V.25–V.29 especially).
- `specs/018-parity-hardening/spec.md` (FR-001..FR-053; R-01..R-13 removals).
- `specs/018-parity-hardening/features.md` (111-row parity dossier, 21/04/2026).
- `specs/018-parity-hardening/testing-research.md` (018 testing blueprint, still binding).
- `specs/017-agent-experience/spec.md` (embeddings, drafts, schedules).
- whatsmeow commit-pinned binding (see `go.sum` pseudo-version).
- `~/NixOS/meta/yolo-labz-release-engineering-{research,plan}.md` (supply-chain source of truth).
