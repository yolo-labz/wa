# Feature 110g — soft-stale watchdog

**Branch**: `149-soft-stale-watchdog`
**Status**: drafted + implemented in same PR (compact spec)
**Source**: 19/05/2026 operator session — the wa-burocracy daemon reported `paired:true connected:true` while the WhatsApp phone showed the device as un-linked. Root cause: when WhatsApp un-links a device server-side without tearing the websocket, whatsmeow never emits `events.LoggedOut`. The daemon keeps holding a dead session ratchet; `health` lies; the only recovery is the 110f `wa pair --reset` flow once an operator notices.

Investigation surfaced a parallel bug: the existing translator in `internal/adapters/secondary/whatsmeow/translate_event.go` audit-panics on every health-signal event whatsmeow emits — `KeepAliveTimeout`, `KeepAliveRestored`, `StreamReplaced`, `ConnectFailure`, `TemporaryBan`, `ClientOutdated`, `ManualLoginReconnect`. Operators get a flood of `AuditPanic` rows that say "unknown whatsmeow event type" for events that are entirely expected. This spec closes both gaps.

## Decision

Translate every whatsmeow health-signal event into a new sealed `domain.ConnectivityHealthEvent`. Wire the existing `Connected` field on the `health` RPC to the live adapter websocket state (not the "session row exists" approximation). Add a `staleSeconds` field — seconds since the EventBridge last observed any event — and a watchdog goroutine that emits a synthetic `HealthSoftStale` event once the threshold is crossed (default 300 s, configurable) plus a `HealthRestored` event when activity resumes.

**The watchdog is detect-and-emit only.** It never auto-Logout, never auto-pair, never touches the session ratchet. Recovery stays operator-driven via the 110f `wa pair --reset` flow. Auto-Logout on soft-stale is rejected because the signal is by design ambiguous (no inbound traffic for 5 minutes is normal at 03:00); a destructive recovery on a false positive would cost the operator a fresh QR scan every quiet night.

## Surface

`health` RPC additive fields, schema stays `wa.health/v1` (backwards-compatible — all new fields are `omitempty`):

```jsonc
{
  "schema": "wa.health/v1",
  "paired": true,
  "connected": true,             // now reflects live websocket state, not "session exists"
  "profile": "burocracy",
  "lastEventTs": 1779212995,
  "sessionSince": 1779213004,
  "staleSeconds": 12,            // NEW — now - lastEventTs (0 if no events yet)
  "staleState": "healthy",       // NEW — "healthy" | "stale" | "unknown"
  "lastHealthSignal": "keepaliveRestored"  // NEW, omitempty — most recent health-signal kind
}
```

`subscribe` channel — new `state.*` events (one per health-signal kind):

```jsonc
{"schema":"wa.event/v1","kind":"state.keepaliveTimeout","ts":...,"detail":"errorCount=3 lastSuccess=2026-05-19T17:42:01Z"}
{"schema":"wa.event/v1","kind":"state.keepaliveRestored","ts":...}
{"schema":"wa.event/v1","kind":"state.streamReplaced","ts":...}
{"schema":"wa.event/v1","kind":"state.connectFailure","ts":...,"detail":"reason=401 LoggedOut"}
{"schema":"wa.event/v1","kind":"state.temporaryBan","ts":...,"detail":"code=2 expire=2026-05-19T20:00:00Z"}
{"schema":"wa.event/v1","kind":"state.clientOutdated","ts":...}
{"schema":"wa.event/v1","kind":"state.manualLoginReconnect","ts":...}
{"schema":"wa.event/v1","kind":"state.softStale","ts":...,"detail":"thresholdSec=300 staleSec=312"}
{"schema":"wa.event/v1","kind":"state.restored","ts":...,"detail":"prevStaleSec=312"}
```

Config — single env var, no CLI flag:

```
WA_SOFT_STALE_THRESHOLD_SEC  # default 300, min 30, max 3600. Set 0 to disable watchdog.
```

## Functional requirements

| ID | Requirement | Verifiable check |
|----|-------------|------------------|
| FR-001 | `translate_event.go` returns `domain.ConnectivityHealthEvent` for `events.KeepAliveTimeout` with `State=HealthKeepAliveTimeout` and a `Detail` of `errorCount=<n> lastSuccess=<rfc3339>`. | Unit test in `translate_event_test.go`. |
| FR-002 | Same for `KeepAliveRestored` → `HealthKeepAliveRestored`, `StreamReplaced` → `HealthStreamReplaced`, `ConnectFailure` → `HealthConnectFailure` (Detail = `reason=<code> <reasonString>`), `TemporaryBan` → `HealthTemporaryBan` (Detail = `code=<n> expire=<rfc3339>`), `ClientOutdated` → `HealthClientOutdated`, `ManualLoginReconnect` → `HealthManualLoginReconnect`. | Table-driven unit test, one row per event kind. |
| FR-003 | The translator no longer returns `sideEffectUnknown` for any of the seven event types above. The default arm of the type switch remains for genuinely-new whatsmeow events. | `git grep -F sideEffectUnknown internal/adapters/secondary/whatsmeow/` — no diff in count, but the seven kinds get explicit cases ahead of `default`. |
| FR-004 | The `health` RPC's `Connected` field reflects the adapter's last-observed websocket state — `true` only between `events.Connected` and `events.Disconnected` (whichever was last). When unset, defaults to the previous "paired & no disconnect seen" approximation so first-startup behaviour is unchanged. | Unit test that drives `state.connected` / `state.disconnected` events and asserts the field flips. |
| FR-005 | The `health` RPC's `staleSeconds` field is `time.Now().Unix() - LastEventUnix()` when `LastEventUnix() > 0`, otherwise `0`. | Unit test that injects clock + bridge. |
| FR-006 | The `health` RPC's `staleState` is `"healthy"` when `staleSeconds < threshold`, `"stale"` when `>= threshold`, `"unknown"` when watchdog is disabled (`threshold == 0`). | Same unit test, three matrix rows. |
| FR-007 | A watchdog goroutine in `cmd/wad` ticks every `min(threshold/3, 60s)` seconds. On the transition healthy→stale it pushes one synthetic `domain.ConnectivityHealthEvent{State: HealthSoftStale, Detail: "thresholdSec=<t> staleSec=<s>"}` onto the EventBridge. On the transition stale→healthy it pushes one `HealthRestored` event. Both transitions debounce — no duplicate emissions while the state is unchanged. | Watchdog goroutine unit test using `synctest`. |
| FR-008 | The watchdog NEVER calls `Client.Logout`, `Client.Disconnect`, or any `session.*` RPC. It only emits events. | Source-grep test: `grep -rn 'Logout\|Disconnect' cmd/wad/watchdog.go` returns zero lines outside imports. |
| FR-009 | `WA_SOFT_STALE_THRESHOLD_SEC` is parsed at startup. Out-of-range or non-integer values fall back to the default 300 s with a single `slog.Warn` line. Empty / unset = default 300. Literal `0` disables the watchdog (no goroutine, no event emission, `staleState="unknown"`). | Startup parse unit test, three rows: empty, invalid, "0". |
| FR-010 | Every health-signal translation appends an `AuditEvent` with `Decision=<state-name>` and `Detail=<translator-detail>`. Synthetic `SoftStale` / `Restored` transitions also audit. | Audit-ring inspection in a watchdog goroutine unit test. |
| FR-011 | The translator's `sideEffectUnknown` default-arm count after this feature equals the count before, minus the seven kinds covered here. The default arm still trips `AuditPanic` for genuinely-new whatsmeow events per the existing FR (clarifications round 2 Q2). | Manual reading of the type switch — only documented in the spec, no test code. |
| FR-012 | The `state.softStale` and `state.restored` synthetic events carry monotonic sequence numbers from the same counter the rest of the EventBridge uses. They are durable in the 10 000-slot ring buffer (feature 018). | Existing ring-buffer integration test extended with one synthetic event. |

## Alternatives rejected

Per Constitution rule 20.

### A. Auto-Logout on soft-stale

Have the watchdog call `Client.Logout(ctx)` once the threshold is crossed, forcing a fresh QR-pair workflow.

**Rejected**: false-positive cost is catastrophic. A genuine quiet period (overnight, weekend) would force the operator to rescan a QR every time the daemon sat idle for five minutes. Recovery from soft-stale is the operator's call via `wa pair --reset` (110f); the daemon's job is to surface the signal, not act on it. CLAUDE.md rule 12 ("no silent fallbacks") cuts the other way too — silent auto-recovery hides the underlying server-side unlink event from operators.

### B. New `wa.health/v2` schema bump

Cut a new schema version so consumers can distinguish v1 (without `staleSeconds`) from v2 (with).

**Rejected**: every new field uses `omitempty`. Consumers that don't read the new fields are unaffected. A schema bump is reserved for breaking changes (removed fields, renamed fields, changed types). Additive evolution stays on v1 per the existing convention in `internal/app/method_health.go`.

### C. Track connectivity via the existing `ConnectionEvent` sum

Add new `ConnectionState` enum variants (`ConnKeepAliveTimeout`, etc.) so health signals reuse the existing event type.

**Rejected**: `ConnectionEvent.State` semantics are "the websocket is in state X right now". Health signals are not websocket states — `KeepAliveTimeout` happens while the websocket is still nominally connected, `StreamReplaced` says the server replaced us but the local websocket hasn't observed disconnect yet. Conflating them breaks the `IsValid()` test that `ConnectionState` only carries the three real websocket states. Cockburn's "port = intent of conversation" (CLAUDE.md rule 21) — connectivity health is a different conversation from websocket lifecycle.

### D. Push the watchdog into the whatsmeow adapter

Run the threshold check inside `internal/adapters/secondary/whatsmeow/adapter.go` so the synthetic event is emitted from the same path as the translated ones.

**Rejected**: the adapter is the wrong layer to own a configurable timer. CLAUDE.md rule 23 ("no infrastructure types in port signatures") cuts both ways — domain-level health policy (the threshold, the debouncer, the audit emission) shouldn't live in the whatsmeow adapter either. `cmd/wad/watchdog.go` is the right home: composition root, has the `EventBridge` handle, can be unit-tested with `synctest` without a real adapter.

## Implementation outline (informative)

| File | Change |
|---|---|
| `internal/domain/event.go` | Add `ConnectivityHealthState` enum (8 values) + `ConnectivityHealthEvent` struct + `isEvent()` marker. |
| `internal/domain/audit.go` | Add `AuditConnectivityHealth` enum constant if not already covered by existing categories — verify against current audit.go. |
| `internal/adapters/secondary/whatsmeow/translate_event.go` | Add 7 explicit case arms ahead of `default`. Each builds the domain event and returns `sideEffectNone`. |
| `internal/adapters/secondary/whatsmeow/translate_event_test.go` | Add table-driven test, one row per event kind. |
| `internal/adapters/secondary/whatsmeow/adapter.go` | Track last-seen websocket state in an `atomic.Bool` (`wsConnected`). Toggle on `events.Connected` / `events.Disconnected` translation. Expose via `WebsocketConnected() bool`. |
| `internal/app/method_health.go` | Add `StaleSeconds`, `StaleState`, `LastHealthSignal` fields. Read `wsConnected` via a new optional `WebsocketProbe` port; fall back to current approximation if port not wired. |
| `internal/app/ports_110g.go` | New file, declares `WebsocketProbe` port (1 method: `WebsocketConnected() bool`). |
| `internal/app/dispatcher.go` | Optional config field `WebsocketProbe`. Threaded to `handleHealth` via dispatcher receiver. |
| `cmd/wad/watchdog.go` | New file. `runSoftStaleWatchdog(ctx, bridge, threshold, audit)`. Ticks, computes staleSeconds, debounces transitions, emits via bridge. |
| `cmd/wad/main.go` | Parse `WA_SOFT_STALE_THRESHOLD_SEC`, kick the watchdog goroutine, register shutdown hook. |
| `internal/app/eventbridge.go` | Add `EmitSynthetic(ev domain.Event)` method — assigns next seq, persists in ring, fans to subscribers + waiters. Same path the translator takes for real events. |

Estimated ~250 LoC across 8 production files + ~200 LoC across 3 test files.

## Test plan

- Unit: `TestTranslate_HealthSignals` (table-driven, 7 rows, asserts `State` + `Detail`).
- Unit: `TestTranslate_NoMoreUnknownForKnownHealthSignals` (asserts `sideEffectUnknown` is not returned for any of the seven).
- Unit: `TestHandleHealth_StaleSecondsArithmetic` (injects clock + EventBridge with fixed `lastEventUnix`).
- Unit: `TestHandleHealth_StaleStateMatrix` ({healthy, stale, unknown} × {threshold=300, threshold=0}).
- Unit: `TestHandleHealth_ConnectedReflectsAdapter` (drives `wsConnected` true→false and asserts the field flips).
- Unit: `TestWatchdog_DebounceTransitions` (`synctest`-driven, simulates idle → threshold → activity, asserts exactly one SoftStale + one Restored event).
- Unit: `TestWatchdog_DisabledThreshold` (`WA_SOFT_STALE_THRESHOLD_SEC=0` → no goroutine started).
- Unit: `TestWatchdog_NeverCallsLogout` (source-grep test, build-time assertion via `go vet`-style scanning in the test).
- Unit: `TestParseThreshold_FallbackOnInvalid` (table: "", "abc", "0", "30", "9999", "300").
- Contract: existing `internal/app/porttest/` extended for the new `WebsocketProbe` port.
- Manual smoke: pair a daemon, kill its websocket TCP connection via `sudo pfctl` from outside, observe `state.keepaliveTimeout` + `state.softStale` events within threshold, restore network, observe `state.keepaliveRestored` + `state.restored`.

## Out of scope

- Auto-recovery (auto-Logout, auto-relink, auto-anything). Detect-and-emit only.
- Phone-side push notification of soft-stale. The `wa-assistant` plugin can subscribe to `state.softStale` and forward to Claude Code via the channel adapter; that lives in the plugin repo, not here.
- A `wa watchdog` standalone subcommand. Operators check `wa health --json | jq .staleState` to verify.
- Tuning the whatsmeow built-in keepalive (`KeepAliveIntervalMin/Max/MaxFailTime`). Those are upstream defaults; this feature observes the events the existing timing produces.
- A configurable per-profile threshold. Single env var applies daemon-wide. Multi-profile users get one threshold per daemon process anyway.

## Success criteria

| Criterion | Metric |
|-----------|--------|
| SC-001 | A daemon paired-but-server-unlinked is observable as soft-stale via `wa health --json` (`staleState=="stale"`) within `threshold+keepaliveCycle` seconds (≤ 360 s at default config). |
| SC-002 | After threshold is crossed, exactly one `state.softStale` event is delivered on the `subscribe` channel — no duplicates while the state persists. |
| SC-003 | When server activity resumes, `staleState` returns to `"healthy"` within one watchdog tick and exactly one `state.restored` event is delivered. |
| SC-004 | A genuinely-new whatsmeow event type (e.g. an unreleased `events.Foo`) still trips the existing `sideEffectUnknown` → `AuditPanic` path. Visibility for the seven covered kinds doesn't cost visibility for future ones. |
| SC-005 | `messages.db`, `audit.log`, `session.db` are unchanged across watchdog start / stop / threshold crossing. No persistence change. |

## References

- Spec 110e — `wa pair --remote` (recovery path operator runs after observing soft-stale).
- Spec 110f — `wa pair --reset` (recovery path that doesn't wipe history).
- whatsmeow `keepalive.go` — upstream emitter of `KeepAliveTimeout` / `KeepAliveRestored`.
- whatsmeow `events.ConnectFailureReason` codes 401/402/403/405/406/409 — mapped via the upstream `.String()` method, no local translation table needed.
- CLAUDE.md rule 12 ("no silent fallbacks") — the load-bearing rule the current `sideEffectUnknown` default-arm satisfies. Translating the seven known kinds keeps the rule intact (still visible, just not panicked).
