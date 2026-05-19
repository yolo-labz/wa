# Feature 110h — media transcribe wiring

**Branch**: `144-media-transcribe-wiring`
**Status**: drafted + implemented in same PR (compact spec)
**Source**: Issue #144 — operator runs `wa media download --transcribe`, the response carries no `transcript` field, `events.db` shows zero `media.transcribed` rows, and a subsequent `wa history` of the chat returns the message body without any decoded speech text. Spec 017 §FR-054/FR-055 mandate a pluggable `Transcriber` port with three adapters; the port type and the three adapters (Whispercpp, Hear, Groq) all exist in tree, but the composition root in `cmd/wad/main.go` never constructs one, `app.DispatcherConfig` has no `Transcriber` field, and `internal/adapters/secondary/whatsmeow/adapter_media.go:118` explicitly discards the `transcribe` argument with `_ = transcribe`. The `transcribe` JSON-RPC flag is a no-op end-to-end.

## Decision

Restore the pipeline end-to-end. The fix is composition-root wiring plus a thin transcribe-and-persist step inside the media use-case layer — **not** inside the whatsmeow adapter. Three architectural rules drive the placement:

- **Cockburn intent-of-conversation** (CLAUDE.md rule 21): "transcribe audio to text" is a separate conversation from "fetch ciphered media bytes". The `Transcriber` port already exists exactly because the media-store conversation should not depend on whisper binaries.
- **No infrastructure in port signatures** (CLAUDE.md rule 23): `MediaStore.Download` accepting `transcribe bool` was the original architectural slip — the bool is a use-case-layer concern. We deprecate it on the port (kept as `_` for the v0 wire but the adapter never reads it) and move the orchestration into `handleMediaDownload`.
- **No silent fallbacks** (CLAUDE.md rule 12): when `transcribe=true` is requested and the Transcriber port is unconfigured, the call MUST return JSON-RPC error `-32115 transcriber not configured`, not silently succeed with an empty transcript field. The existing precedent is `-32114 not_business_account` from feature 018.

Transcripts are stored once per `sha256`, keyed on the content-addressed media identity, in a new `media_transcripts` table (v5→v6 migration). `media.resolve` and `media.download` hydrate `MediaObject.Transcript` from this table; `thread.get` joins it via the sha256 extracted from each message's `raw_proto`. The synthetic `media.transcribed` event surfaces on the `subscribe` channel with a `state.*`-style envelope so operators (and the future `wa-assistant` plugin) can react to fresh transcripts in real-time.

The transcribe call is **synchronous** within the `media.download` handler — operators already wait for the network fetch + sha verify, and adding a separate async path doubles surface area for a marginal latency win. Cancellation propagates via the request context.

## Surface

JSON-RPC — `media.download` params extended only by what already shipped (the `transcribe` bool already exists in `mediaDownloadParams`; this spec wires it up). New env vars + new event kind:

```jsonc
// media.download response — transcript field already declared, now populated
{
  "schema": "wa.media.object/v1",
  "object": {
    "sha256": "abc123...",
    "path": "/Users/notroot/Library/Caches/wa/media/sha256/ab/c123....ogg",
    "mime": "audio/ogg",
    "size": 18420,
    "durationSeconds": 7,
    "transcript": "vamos almoçar às doze",   // NEW path-of-data (already declared on wire)
    "fetchedAt": 1779300000
  },
  "cached": false,
  "bytesFetched": 18420
}

// subscribe channel — NEW event kind, monotonic seq from EventBridge
{
  "schema": "wa.event/v1",
  "kind": "media.transcribed",
  "seq": 42,
  "ts": 1779300012,
  "sha256": "abc123...",
  "messageId": "3EB0...",
  "lang": "pt",
  "chars": 21,
  "adapter": "whispercpp"
}
```

Config — three env vars on `wad`, no CLI flag:

```
WA_TRANSCRIBER       # one of: whispercpp | hear | groq | "" (disabled, default)
WHISPER_BIN          # path or basename; default "whisper-cli" then "main"
WHISPER_MODEL_PATH   # required when WA_TRANSCRIBER=whispercpp
WA_GROQ_API_KEY      # required when WA_TRANSCRIBER=groq; falls back to $XDG_CONFIG_HOME/wa/groq.env per existing transcribe.LoadGroqKey
```

Default `WA_TRANSCRIBER=""` keeps the daemon transcriber-free — `media.download --transcribe=true` returns `-32115` until the operator opts in.

## Functional requirements

| ID | Requirement | Verifiable check |
|----|-------------|------------------|
| FR-001 | `cmd/wad/main.go` parses `WA_TRANSCRIBER` at startup and constructs the matching adapter: `whispercpp` → `transcribe.NewWhispercpp(WHISPER_BIN, WHISPER_MODEL_PATH)`, `hear` → `transcribe.NewHear(WHISPER_BIN)` (Darwin build tag), `groq` → `transcribe.NewGroq(transcribe.LoadGroqKey($XDG_CONFIG_HOME/wa/<profile>/groq.env))`. Construction failure logs at `slog.Warn` and leaves the daemon transcriber-less (callers get `-32115`); does NOT block daemon boot. | Unit test on the new `selectTranscriber(env, log)` helper, table-driven (5 rows: unset, whispercpp ok, whispercpp missing model, hear ok, groq disabled). |
| FR-002 | `app.DispatcherConfig` gains a `Transcriber app.Transcriber` field. `app.NewDispatcher` stores it. When nil, `transcribe=true` requests return `-32115 transcriber_not_configured` (numeric JSON-RPC code in the `-32100..-32199` policy range). | Dispatcher unit test that calls `media.download` with a nil Transcriber and asserts the typed error. |
| FR-003 | `handleMediaDownload` invokes the Transcriber after the underlying `MediaStore.Download` returns, only when **all three** conditions hold: (a) the request has `transcribe=true`, (b) the resolved `MediaObject.MimeDetected` (NOT `MimeAdvertised`) starts with `audio/`, (c) `d.transcriber != nil`. Non-audio downloads are silently un-transcribed (the flag is a hint, not a fatal mismatch — matches FR-054 "voice notes" scope). | Unit test with two rows: audio mime + flag → calls Transcriber; image mime + flag → does not call Transcriber. |
| FR-004 | Transcripts are persisted in a new `media_transcripts` table (v5→v6 ALTER-additive migration, idempotent on second run). Columns: `sha256 BLOB PRIMARY KEY` (32-byte raw, matches the on-disk path scheme), `transcript TEXT NOT NULL`, `lang TEXT NOT NULL DEFAULT ''`, `adapter TEXT NOT NULL`, `created_at INTEGER NOT NULL`. Upsert via `INSERT ... ON CONFLICT(sha256) DO UPDATE SET transcript=excluded.transcript, lang=excluded.lang, adapter=excluded.adapter, created_at=excluded.created_at`. The upsert lets a re-transcribe with a better adapter overwrite an earlier result. | Migration test asserts `PRAGMA user_version=6` post-migrate + table exists with the four columns; second-call idempotency test. |
| FR-005 | The `MediaStore.Resolve(sha)` path hydrates `MediaObject.Transcript` from `media_transcripts` when present. `MediaStore.Download` also hydrates from the table on the cached-hit branch so a previously-transcribed payload returns its transcript even when `transcribe=false` (zero-cost — table lookup is by primary key). | Two unit tests: Resolve-after-transcribe returns transcript; Download cached-hit returns transcript. |
| FR-006 | `thread.get` includes the transcript per message that has audio media. Implementation: for each row whose `raw_proto` parses to a `Message` with an audio payload, extract sha256 via the existing `extractMediaHints`, query `media_transcripts` by that sha256, attach to the wire payload as `mediaTranscript`. Non-audio messages remain unaffected. | Thread.get unit test with one mixed-conversation seeded with an audio message + transcript row. |
| FR-007 | After a successful transcribe, the dispatcher pushes a `domain.MediaTranscribedEvent{SHA256, MessageID, Lang, Chars, Adapter}` onto the EventBridge via `EmitSynthetic`. The event flows through the existing ring buffer (events.db) AND the live subscribe fan-out. The kind translates to `media.transcribed` per the `translateDomainEvent` switch. | Event-bridge unit test: emit synthetic + receive on subscribed channel with `Type="media.transcribed"`. |
| FR-008 | Transcribe errors (binary missing, model missing, network failure on Groq, ctx cancelled) propagate as JSON-RPC error `-32116 transcribe_failed` carrying the wrapped Go error in `data.detail`. The media payload is still returned successfully — the download succeeded, the transcribe step did not. Operator can retry with `--transcribe` after fixing config. | Unit test injects a failing Transcriber and asserts response carries the object + error envelope shape. |
| FR-009 | `audio/*` detection uses `MimeDetected` (the `net/http.DetectContentType` result), not `MimeAdvertised` (the sender's claim). A sender lying about `audio/ogg` to push a `.exe` does NOT trigger the transcribe path. This is the existing `MediaObject.MimeMismatch()` discipline — extended to transcribe gating. | Unit test: payload sniffed as `application/octet-stream` + advertised `audio/ogg` → no transcribe call. |
| FR-010 | `cmd/wad install-service` and the launchd/systemd unit templates document `WA_TRANSCRIBER`/`WHISPER_BIN`/`WHISPER_MODEL_PATH`/`WA_GROQ_API_KEY` as optional environment variables. | `wad install-service --dry-run` golden file diff includes the new env-var stanza. |
| FR-011 | Every transcribe attempt emits an `AuditEvent` with `Action=media.transcribe`, `Decision="ok"|"refused"|"failed"`, `Detail="adapter=<x> sha=<8hex>... chars=<n> lang=<x>"`. The audit row lands in `audit.log` per the existing AuditLog port. | Audit-log inspection test in the use-case unit test. |
| FR-012 | The Transcriber call honours `ctx` — when the caller cancels (socket disconnect, `wa media download` Ctrl-C), the spawned `whisper-cli`/`hear` process is killed via `exec.CommandContext`. The existing adapters already do this; FR-012 binds the use-case layer to NOT swallow `ctx.Err()`. | Unit test that cancels ctx mid-transcribe and asserts `errors.Is(err, context.Canceled)`. |

## Alternatives rejected

Per Constitution rule 20.

### A. Push transcribe into the whatsmeow `MediaAdapter.Download`

Make the adapter aware of the Transcriber port — keep `MediaStore.Download(transcribe bool)` and have the adapter shell out after the sha-verify step.

**Rejected**: violates CLAUDE.md rule 23 ("no infrastructure types in port signatures") in spirit — the adapter would need to know about whisper.cpp paths and Groq tokens, dragging two more dependencies into the secondary boundary. Cockburn rule 21 reads the same way: "fetch ciphered bytes" and "extract speech" are two conversations. Worse, having both Transcriber wiring AND raw download wiring inside the adapter doubles the test fixture surface — the existing in-memory `MediaStore` fake would need transcribe support for tests that don't care.

### B. Async transcribe via a separate `transcribe` queue

Run transcribe in a goroutine pool, return `media.download` immediately, push `media.transcribed` events when each finishes.

**Rejected**: doubles surface area for marginal benefit. Operators already wait for the network fetch (1–3 s on 4G); whisper-cli `base` adds ~2 s on a 7-second voice note (M-series CPU). Total <5 s is well within the existing `wa media download` UX. Async would require: (a) a queue store, (b) a separate worker goroutine, (c) a delivery contract for `media.transcribed` independent of subscriber liveness, (d) restart-resume logic. None of that pays for itself when the synchronous path is fast enough. The future `media.transcribe(sha)` standalone RPC (out of scope) is the place to add async if someone wants to retroactively transcribe a backfill of cached payloads.

### C. Per-message transcript column on `messages` (v5→v6 ALTER ADD COLUMN)

Add `transcript TEXT NULL` to `messages` and write the transcript on the row that owns the media reference.

**Rejected**: violates FR-054's "stored once per sha256". A voice note forwarded across three chats would produce three rows with the same transcript text — wasting space and making the FTS index three times as fat for a single utterance. The content-addressed `media_transcripts` table is the correct deduplication boundary, identical to how the on-disk cache itself is deduplicated by sha256.

### D. Embed the transcribe call inside `media.resolve` instead of `media.download`

Have `media.resolve(sha)` lazy-transcribe when the cached payload has no transcript yet.

**Rejected**: `media.resolve` is contractually network-free per FR-050 (the existing port doc string). Running whisper.cpp inside it would surprise callers who expect a sub-millisecond response. The transcribe step belongs with the download step (paid once, when the operator explicitly opts in) or in a future standalone `media.transcribe(sha)` RPC (out of scope).

## Implementation outline (informative)

| File | Change |
|---|---|
| `cmd/wad/transcriber.go` | New file. `selectTranscriber(env map[string]string, profile, log) (app.Transcriber, error)` switch on `WA_TRANSCRIBER`. Returns `nil, nil` for unset/empty. Logs warnings on construction failure but returns `(nil, nil)` — daemon boots transcriber-less, callers get `-32115`. |
| `cmd/wad/transcriber_test.go` | Table-driven unit test, 5 rows. |
| `cmd/wad/main.go` | One call to `selectTranscriber` after audit-log wiring. Pass the result into `app.DispatcherConfig.Transcriber`. |
| `internal/app/dispatcher.go` | Add `Transcriber app.Transcriber` field on `Dispatcher` struct + `DispatcherConfig`. Plumb through `NewDispatcher`. |
| `internal/app/method_media.go` | Extract the transcribe-and-persist orchestration into `(d *Dispatcher) maybeTranscribe(ctx, obj, msgID, requested) (mediaObjectView, error)`. Call from `handleMediaDownload` after `viewMediaObject`. Return new `-32115`/`-32116` typed errors for missing-transcriber / transcribe-failed. |
| `internal/app/errors.go` | Two new constants: `ErrTranscriberNotConfigured` (`-32115`), `ErrTranscribeFailed` (`-32116`). |
| `internal/domain/event.go` | Add `MediaTranscribedEvent` struct + `isEvent()` marker. Fields: `ID EventID; TS time.Time; SHA256 [32]byte; MessageID MessageID; Lang string; Chars int; Adapter string`. |
| `internal/app/eventbridge.go` | Add `case domain.MediaTranscribedEvent: return Event{Type: "media.transcribed", Payload: evt}` in `translateDomainEvent`. |
| `internal/adapters/secondary/sqlitehistory/migrate_v6.go` | New file. `migrateV6(ctx, db)` creates `media_transcripts` + bumps `PRAGMA user_version=6`. Idempotent via `CREATE TABLE IF NOT EXISTS`. Reversal in `DownV6`. |
| `internal/adapters/secondary/sqlitehistory/migrate.go` | Add `if version < 6 { ... migrateV6 ... }` step. Update existing migration-history audit. |
| `internal/adapters/secondary/sqlitehistory/transcript_store.go` | New file. `(s *Store) UpsertTranscript(ctx, sha, transcript, lang, adapter)` + `(s *Store) GetTranscript(ctx, sha) (string, error)` returning `("", os.ErrNotExist)` on miss. |
| `internal/app/ports_017.go` | Add `TranscriptStore` port (1 method `Upsert`, 1 method `Get`). Wired from `sqlitehistory.Store` like `idempotency`. Note: Cockburn rule 21 — "store transcript by sha256" is a separate conversation from "store message rows". |
| `internal/adapters/secondary/whatsmeow/adapter_media.go` | Replace `_ = transcribe // ...` with a `_ = transcribe // moved to dispatcher per spec 110h` comment + keep the bool on the port for v0 wire stability (callers still pass it; the adapter just ignores it). Hydrate `MediaObject.Transcript` from the new `TranscriptStore` port on cached-hit + on `Resolve`. |
| `internal/app/method_thread.go` | Existing `thread.get` handler — for each message row with audio media, lookup transcript via `TranscriptStore.Get` keyed on extracted sha256, attach to the wire payload. |
| `internal/adapters/secondary/whatsmeow/translate_event.go` | No change — `MediaTranscribedEvent` is synthetic, emitted from the dispatcher, not translated from whatsmeow. |

Estimated ~350 LoC across 12 production files + ~250 LoC across 5 test files.

## Test plan

- Unit: `TestSelectTranscriber_Matrix` (5 rows: unset / whispercpp ok / whispercpp missing model / hear / groq disabled).
- Unit: `TestHandleMediaDownload_TranscribeFlagWithoutPort` (asserts `-32115`).
- Unit: `TestHandleMediaDownload_AudioMimeTriggersTranscribe` (in-memory Transcriber records the call).
- Unit: `TestHandleMediaDownload_NonAudioMimeSkipsTranscribe`.
- Unit: `TestHandleMediaDownload_MimeAdvertisedSpoof` (advertised audio/ogg, detected octet-stream → no transcribe).
- Unit: `TestHandleMediaDownload_TranscribeFailureReturns32116`.
- Unit: `TestHandleMediaDownload_TranscribeCancellationPropagates`.
- Unit: `TestMigrateV6_CreatesTranscriptsTable` + idempotency (second call is a no-op).
- Unit: `TestMigrateV6_Down` (reverts user_version to 5 and drops table).
- Unit: `TestTranscriptStore_UpsertGetRoundtrip`.
- Unit: `TestTranscriptStore_UpsertOverwritesEarlierResult`.
- Unit: `TestMediaResolve_HydratesTranscript`.
- Unit: `TestMediaDownloadCached_HydratesTranscript`.
- Unit: `TestThreadGet_IncludesTranscriptForAudioMessages`.
- Unit: `TestEventBridge_MediaTranscribedSynthetic` (asserts the kind translates to `media.transcribed`).
- Unit: `TestAuditLog_TranscribeRowsRecorded`.
- Contract: `internal/app/porttest/` extended for the new `TranscriptStore` port.
- Manual smoke: receive a voice note from a paired phone, run `wa media download --transcribe`, assert the transcript appears in (a) the JSON response, (b) `wa subscribe --kind media.transcribed`, (c) `wa history` of the chat, (d) a second `wa media resolve <sha>` call.

## Out of scope

- A `media.transcribe(sha)` standalone RPC (the spec's "lazy transcribe an already-cached payload" path). Useful for backfill, but not required for the regression fix.
- Streaming partial transcript chunks. Whisper emits per-segment but our wire is final-text-only — matches the existing Transcriber port shape.
- Per-contact transcribe toggle in `allowlist.toml` (e.g. "auto-transcribe everything from Mom, never for noisy group X"). The `TranscribePolicy` helper exists in `app/transcribe_policy.go` for inbound auto-transcribe but inbound is feature 017's scope, not this regression fix. Plumbing the policy into inbound is a v0.6 follow-up.
- Language hinting on the wire (the `lang` arg on `Transcriber.Transcribe` stays adapter-internal). Adding `lang` to the JSON-RPC params is a follow-up.
- Multi-profile audit of transcriber configuration (one daemon process = one transcriber).
- Encryption of the `media_transcripts` table at rest. FileVault / LUKS is the boundary, identical to `messages.db` discipline (CLAUDE.md §Filesystem layout).

## Success criteria

| Criterion | Metric |
|-----------|--------|
| SC-001 | `wa media download --transcribe <messageId>` against a paired daemon with `WA_TRANSCRIBER=whispercpp` returns a JSON response with a non-empty `transcript` field within 5 seconds for a 7-second voice note (M-series CPU, ggml-base model). |
| SC-002 | The same call emits exactly one `media.transcribed` event observable on `wa subscribe --kind media.transcribed`. Re-running the same `wa media download` against a cached payload re-emits the event only if `transcribe=true` AND the transcript was previously absent — repeated transcribes of an already-transcribed sha are no-ops at the use-case layer (FR-005 hydrates from the store, the Transcriber port is NOT invoked again). |
| SC-003 | `wa history <chat> --json` post-transcribe shows the transcript text inside the message row that owned the audio (FR-006 path). |
| SC-004 | With `WA_TRANSCRIBER=""` (default), `wa media download --transcribe` returns JSON-RPC error `-32115 transcriber_not_configured` and the payload is still cached on disk. The download itself succeeds; only the transcribe step refuses. |
| SC-005 | With `WA_TRANSCRIBER=whispercpp` but `WHISPER_MODEL_PATH=/does/not/exist`, the daemon boots, logs a single `WARN` line, and serves transcribe-less. `wa media download --transcribe` returns `-32115`. The daemon does NOT crash. |
| SC-006 | `messages.db`, `audit.log`, `session.db` schema diffs are exactly the new `media_transcripts` table + `user_version=6` bump. No other writes touched by this feature. |
| SC-007 | `golangci-lint run` stays green — the existing `core-no-whatsmeow` depguard rule continues to refuse whatsmeow imports inside `internal/domain` and `internal/app`. The Transcriber port stays whatsmeow-free. |

## References

- Spec 017 §FR-054, §FR-055 — Transcriber port mandate; three adapters in scope.
- Spec 018 §FR-013 — backup-before-migrate contract the v5→v6 step inherits.
- `internal/app/transcribe_policy.go` — existing inbound auto-transcribe policy (out of scope here; reserved for v0.6 inbound work).
- whisper.cpp `whisper-cli` flags `-otxt -of <stem> -nt -l <lang>` — used by the existing Whispercpp adapter.
- `hear` (Darwin) `-i -s -l` — used by the existing Hear adapter.
- Groq Whisper-compatible HTTP API `/openai/v1/audio/transcriptions` — used by the existing Groq adapter.
- CLAUDE.md rule 12 (no silent fallbacks) — drives the `-32115`/`-32116` typed errors over a silent empty-transcript path.
- CLAUDE.md rule 21 (Cockburn intent-of-conversation) — drives transcribe orchestration into the use-case layer, not the adapter.
- CLAUDE.md rule 23 (no infrastructure in port signatures) — drives transcript persistence into a separate `TranscriptStore` port.
