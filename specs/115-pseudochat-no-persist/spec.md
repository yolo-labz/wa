# Feature 115 — no persistence for pseudo-chat traffic

**Branch**: `115-spec-record` (spec record for code landed in #388; authored
design-first on 21/09/2026 before the implementation)
**Status**: implemented and merged (#388, squash `ebb720c`) — this file is the
design record the compact-spec convention puts in the same PR; `specs/` is
gitignored (`.gitignore:39`, scrub rationale) so a plain `git add` cannot see it
and the code PR landed without it.
**Source**: live measurement over a read-only copy of `wa-personal`'s store,
21/09/2026. The largest chat in that store is one nobody reads:

| chat | rows | notes |
|---|---|---|
| `status@broadcast` | **2,696** of 32,450 | 31 distinct senders, **zero** from the owner; 1,350 carry a media reference; `raw_proto` totals **4.35 MB** (avg 1.6 KB/row); window 28/07 → 21/09/2026 |
| `0@s.whatsapp.net` | 5 | the server's own notice chat |
| `1788957129@broadcast` | 2 | an orphan broadcast list |

## Problem

Feature 009 — FR-001 (`internal/adapters/secondary/whatsmeow/adapter_inbound.go:93`)
persists every inbound `MessageEvent`. That is correct for conversations and wrong for
these three chats:

- **No reader exists.** No CLI verb surfaces them, no webhook/flow consumes them
  (`wa_opportunity_sweep.py:523` explicitly excludes `status@broadcast`), and the
  assistant bridge matches on member/chat JIDs rather than on a broadcast chat.
- **They are third-party content.** Status updates carry contact-authored
  thumbnails/captions; the daemon retains them with no reader and no retention rule.
- **They are the biggest chat by row count**, and `media gc` cannot see rows — it only
  trims the media store, so the rows stay behind their (now possibly absent) media.
- **There is no knob to stop it.** `status@broadcast` appears nowhere in `internal/` or
  `cmd/`; the four persistence sites — `adapter_inbound.go:174`,
  `history_sync.go:166`, `send.go:83`, `history.go:129` — each write with no policy of
  their own.

Consequence: a one-off `wa purge --chat status@broadcast` regrows from the next status
delivery. This feature is the **prevention** half. Deleting the 2,696 historical rows is
**out of scope** (destructive, owner-gated; see Out of scope).

## Decision

One predicate in the domain, applied on every persistence path.

1. `domain.IsNonConversationChat(chatJID string) bool` (`internal/domain/jid.go`) —
   true for `@broadcast` (WhatsApp Status **and** broadcast lists) and for
   `0@s.whatsapp.net` (server notices).
2. **It classifies the string form, deliberately.** `domain.Parse` refuses `@broadcast`
   on sight (`ErrBroadcastForbidden`, `internal/domain/jid.go:120` — CLAUDE.md §Safety
   "no broadcast lists ever"), so a predicate on `domain.JID` is *unreachable* for the
   very chats this feature exists to catch: a parse-based gate would fail open on
   `status@broadcast` and silently store every row it was meant to stop. The
   classification must be able to name a chat the domain refuses to construct.
3. Each of the four persistence sites guards with that one predicate. It **fails
   open**: an unparseable or unrecognised chat is still stored, so the gate can never
   silently drop real traffic (CLAUDE.md R30 — cut at the first decisive step, not at
   the loudest one).
4. Non-goals are explicit: the event pipeline (webhook fan-out, SSE, socket subscribers)
   and the media store are untouched. A consumer that wants status traffic does not
   exist today; if one is added, it belongs in a new spec that re-opens this decision.

## Surface

| Layer | File | Change |
|---|---|---|
| domain | `internal/domain/jid.go` | `IsNonConversationChat(chatJID string) bool` + doc comment citing this spec and the `ErrBroadcastForbidden` reason |
| live inbound | `internal/adapters/secondary/whatsmeow/adapter_inbound.go` | guard before `InsertRawInteractive` (contact mirror + audit stay) |
| history sync | `internal/adapters/secondary/whatsmeow/history_sync.go` | guard before `InsertRaw` |
| own sends | `internal/adapters/secondary/whatsmeow/send.go` | guard before `InsertRaw` |
| import | `internal/adapters/secondary/whatsmeow/history.go` | guard before `InsertDomainMessages` |

## Functional requirements

| ID | Requirement | Verifiable check |
|----|-------------|------------------|
| FR-115-1 | `domain.IsNonConversationChat` is true for `status@broadcast`, any `<n>@broadcast`, and `0@s.whatsapp.net`; false for a user JID, a LID, a group, a newsletter, the zero JID, an empty string, a non-JID string and an unknown server. | `TestIsNonConversationChat`, table-driven, both directions (in `jid_test.go`, next to the `Parse` tests that define the same namespaces). |
| FR-115-2 | `persistInboundMessage` does not call the history store for a chat that the predicate matches. Nothing else about the call changes — same audit, same contact mirror, same event emission. | `TestPersistInbound_PseudoChatIsNotStored` asserts zero insert calls for `status@broadcast` and `0@s.whatsapp.net`, and that a normal chat still inserts (mirroring `persist_interactive_test.go`'s harness). |
| FR-115-3 | History sync (`history_sync.go`), our own sends (`send.go`) and history import (`history.go`) honour the same predicate — a fifth writer cannot be added without meeting it. | `grep -n 'IsNonConversationChat(' internal/adapters/secondary/whatsmeow/*.go` shows one hit per file, 4 files. |
| FR-115-4 | The predicate fails **open**: an empty, malformed or unknown-server chat string is treated as a conversation and still persists. | `TestIsNonConversationChat` rows: `""`, `"not a jid"`, `"123@unknown.server"`, `"@broadcast"` (empty user). |
| FR-115-5 | No behaviour change for conversation traffic: every existing test in the package passes unmodified. | `go test -race -shuffle=on -count=1 ./internal/...` green with no edits to pre-existing tests. |

## Alternatives rejected

Per CLAUDE.md rule 20.

### A. Filter inside `sqlitehistory.Store`

Rejected: the store is an adapter that mirrors *a* history, not the policy holder. A
store-level filter would also apply to any future legitimate writer of those rows
(e.g. an operator import) and hides the decision from the message path that owns it.

### B. Filter at read time (CLI/webhook)

Rejected: it fixes the symptom (surfacing) and not the cause (retention). The rows keep
growing — 2,696 rows is the measurement of that policy already failing.

### C. A config knob (default on, `WAD_…_STATUS=off`)

Rejected: an unused knob is a second code path with no second use case. The decision is
"the daemon has no reader for these chats"; when that changes, it changes in a spec.

### D. Glob every chat whose server is not `s.whatsapp.net`/`g.us`/`lid`

Rejected as over-broad: `@newsletter` (channels) and `@bot` are real surfaces the daemon
does read for some verbs. The rule names its members explicitly instead.

### E. A `domain.JID.IsNonConversation()` predicate (the first design in this spec)

Rejected after reading `domain.Parse`: it refuses `@broadcast` with `ErrBroadcastForbidden`
(`internal/domain/jid.go:120`, Spec 108 — "no broadcast lists ever"). A predicate on
`domain.JID` is therefore **unreachable** for `status@broadcast` and every broadcast list:
`toDomain` errors, the gate would fail open, and the feature would ship green tests while
storing every row it was meant to stop. The classifier must live on the string form the
event pipeline actually carries.

## Test plan

- `internal/domain/jid_test.go` — `TestIsNonConversationChat` (FR-115-1/4).
- `internal/adapters/secondary/whatsmeow/persist_filter_test.go` —
  `TestPersistInbound_PseudoChatIsNotStored` (FR-115-2), reusing the existing
  `newTestAdapter` / `auditHistoryContainer` harness from `persist_interactive_test.go`.
- Full gate: `make test` (`go test -race -count=1 ./...`) plus `make lint`.

## Out of scope

- **Deleting the 2,696 historical rows.** Destructive; the owner holds that call.
  An audit receipt (row count, sender count, window, `sha256` of the sorted ids, no
  content) is kept outside the repo at `~/.local/state/wa-hygiene/status-broadcast-2026-09-21.json`.
- **The media store.** `media gc` already trims it on its own cadence.
- **Event-pipeline filtering.** Webhook/SSE consumers keep seeing what they see today.
- **Retroactive cleanup of `history_sync` imports** — the gate is forward-only, by design.

## Success criteria

| Criterion | Metric |
|---|---|
| SC-001 | After deploy, `status@broadcast` row count stops growing; a status received post-deploy adds zero rows. |
| SC-002 | Conversation traffic is unaffected: `rows(chat)` for any `@s.whatsapp.net`/`@g.us`/`@lid` chat is unchanged by this feature. |
| SC-003 | No test in the package required modification, and `go test -race -shuffle=on -count=1 ./...` is green. |
| SC-004 | An unparseable chat string still persists (the gate cannot lose real traffic). |
| SC-005 | The four call sites are the only writers, and each one calls the predicate: `grep -c 'IsNonConversationChat(' internal/adapters/secondary/whatsmeow/*.go` shows 4 hits in 4 files. |
