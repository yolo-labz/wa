# Feature 110f — `wa pair --reset`

**Branch**: `148-wa-pair-reset`
**Status**: drafted + implemented in same PR (compact spec)
**Source**: 19/05/2026 operator session — `wa-burocracy` daemon reported `paired:true connected:true` but the WhatsApp phone showed the device as un-linked. Existing recovery paths were unusable:

- `wa session logout-all` → broken on pinned whatsmeow commit (`Client.LogoutAll` missing).
- `wa panic` → **full R-07 wipe** (kills `messages.db`, `audit.log`, lockfile).

There was no "unlink THIS device server-side + clear ratchet, keep history" path. This feature adds one.

## Decision

Add a `--reset` boolean flag on `wa pair`. When set, the CLI first calls a new `session.logout` JSON-RPC method, then proceeds with the normal pair flow. The daemon method wraps **whatsmeow `Client.Logout(ctx)`**, which:

1. Sends a server-side IQ to WhatsApp asking to un-link this device.
2. Disconnects the websocket.
3. Calls `cli.Store.Delete(ctx)` — deletes the `whatsmeow_device` row + identity / sender-key tables from the session SQLite.

`messages.db`, `audit.log`, `contacts.db`, `drafts.db`, `events.db` live in **different SQLite files** (separate `*.db` per package); `Store.Delete` does NOT touch them. Chat history, audit chain, draft queue, ring-buffer events all survive.

After Logout returns, the daemon's `*whatsmeow.Client` has no `Store.ID`. The next `pair` RPC sees `Store.ID == nil` and proceeds with a fresh QR (or phone-code) handshake.

## Surface

```bash
# Local daemon
wa pair --reset

# With QR in browser
wa pair --reset --browser

# Phone-code
wa pair --reset --phone +5511999999999

# Remote daemon over SSH (combines with 110e --remote)
wa pair --remote ProxMox.Dokku:wa-burocracy --reset
```

## Functional requirements

| ID | Requirement | Verifiable check |
|----|-------------|------------------|
| FR-001 | `wa pair --reset` calls `session.logout` JSON-RPC before `pair` JSON-RPC. | Manual smoke + unit test that captures dispatcher calls. |
| FR-002 | `session.logout` method wraps `Client.Logout(ctx)` only — does NOT touch `messages.db` / `audit.log` / `contacts.db` / `drafts.db` / `events.db`. | `git diff` on those files = empty after running the RPC against a paired daemon. |
| FR-003 | If the daemon has no current device (`Store.ID == nil`), the RPC returns "not paired" error and skips the upstream IQ. | Unit test: empty store → error path. |
| FR-004 | An `AuditLogout` entry is appended on every successful logout. Failure does not audit (success-only per CLAUDE.md rule 12). | Audit-ring inspection after success / failure. |
| FR-005 | `--reset` propagates through the 110e SSH chain when both `--remote` and `--reset` are set. | `wa pair --remote host:app --reset` → `ssh ... wa pair --reset` in the in-container invocation. Argv-shape test covers this. |
| FR-006 | `wa pair --reset` is idempotent: calling it twice in a row does not break the daemon. | Dispatcher's `idempotentCall` wraps `session.logout`. |
| FR-007 | The `wa panic` command remains the documented "full wipe" path and is NOT replaced by this feature. Both paths coexist. | `wa --help` shows both `pair --reset` and `panic` with distinct descriptions. |
| FR-008 | A new `domain.ActionLogout` is added to the allowlist action enum; `session.logout` is mapped to it. | `grep ActionLogout` shows the new constant + mapping. |

## Alternatives rejected

Per Constitution rule 20.

### A. Extend `wa panic` with a `--keep-history` flag

Add a boolean to `wa panic` that skips the messages.db / audit.log file removal.

**Rejected**: `wa panic` is intentionally a single-purpose destructive op per CLAUDE.md "Decisions already locked in". Overloading it with a non-destructive variant blurs the operator-mental-model contract. A separate flag on `wa pair` is clearer: `--reset` = "I want to re-pair this device", with non-destructive guarantee.

### B. Wait for upstream whatsmeow to expose a `Client.LogoutCurrent` helper

Existing `wa session logout-all` blocks on `Client.LogoutAll` (absent on pinned commit). Wait for upstream and use whichever helper they add.

**Rejected**: upstream timeline is unknowable. `Client.Logout(ctx)` is ALREADY present (and is in fact called by `panic.go` step 1). Just expose it as its own RPC; no upstream wait needed.

### C. Add a sibling RPC `device.unlink` instead of `session.logout`

New port + adapter + RPC under a `device.*` namespace.

**Rejected**: the existing `SessionTerminator` port (FR-031) already represents the "logout conversation". Extending it with `Logout(ctx)` is one method on one port; introducing a new `Device*` port group fragments the architecture for no semantic gain. Cockburn's "port = intent" rule (CLAUDE.md §IV.21) keeps both methods in the same port.

## Implementation outline (informative)

| File | Change |
|---|---|
| `internal/domain/action.go` | Add `ActionLogout` constant + `"logout"` wire tag. |
| `internal/domain/audit.go` | Add `AuditLogout` enum + `"logout"` audit-action-name. |
| `internal/app/ports_018.go` | Extend `SessionTerminator` interface with `Logout(ctx) error`. |
| `internal/adapters/secondary/whatsmeow/logoutall.go` | Add `(*LogoutAllAdapter).Logout(ctx)` — wraps `client.Logout(ctx)`, writes `AuditLogout` on success. |
| `internal/app/method_logout_all.go` | Add `handleSessionLogout` + `doSessionLogout` mirroring the logout-all pattern. |
| `internal/app/dispatcher.go` | Register `"session.logout"` in the method table. |
| `internal/app/allowlist_middleware.go` | Map `"session.logout"` → `domain.ActionLogout`. |
| `cmd/wa/cmd_pair.go` | Add `pairReset bool` cobra flag. RunE short-circuit: if set, call `session.logout` RPC first. |
| `cmd/wa/cmd_pair_remote.go` | `buildPairExtraFlags()` appends `--reset` to the SSH-chain argv when set. |

No daemon-state-machine change, no new ports beyond extending one method, no schema migration. ~150 LoC across 9 files.

## Test plan

- Unit: `TestLogoutAdapter_NotPaired` (store.ID nil → error).
- Unit: `TestLogoutAdapter_HappyPath` (fake whatsmeow client records `LogoutCalls`).
- Unit: `TestPairReset_CallsSessionLogoutBeforePair` (cobra RunE invocation sequence).
- Manual smoke: `wa pair --remote ProxMox.Dokku:wa-burocracy --reset` → SSH chain runs `wa pair --reset` → daemon unlinks → fresh QR renders in operator's terminal.

## Out of scope

- A `wa session logout` standalone subcommand. The flag on `pair` is sufficient for the documented re-pair flow. If operators ask for the standalone later, trivially add `wa session logout` calling the same RPC.
- A confirmation prompt before `--reset`. The operation is non-destructive (history preserved); no prompt needed. Operators who want destructive use `wa panic` (which is also no-prompt — destruction is the explicit ask).

## Success criteria

| Criterion | Metric |
|-----------|--------|
| SC-001 | Operator can re-pair a stuck daemon in ≤ 60 s end-to-end from `wa pair --remote host:app --reset` to QR scan. |
| SC-002 | `messages.db` byte-identical before and after a successful `--reset` (sha256). |
| SC-003 | Daemon returns to `paired:true connected:true` within 5 s after the new QR is scanned. |

## References

- Spec 110c — REST CLI mode (parent of `--remote` global flag).
- Spec 110e — `wa pair --remote` SSH-chain UX (parent of pair-remote flow).
- whatsmeow `Client.Logout(ctx)` — upstream method that does the work.
- `~/Documents/Notes/wa-110e-loop.md` — sister-feature loop log.
