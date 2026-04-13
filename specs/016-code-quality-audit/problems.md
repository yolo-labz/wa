# Code Quality Audit: Problems Found

**Audit date**: 2026-04-13
**Scope**: All Go source files in `cmd/`, `internal/`, and project-wide patterns
**Method**: 5-agent parallel codebase exploration swarm

---

## Summary

| Layer | Critical | High | Medium | Low | Total |
|---|---|---|---|---|---|
| Domain (`internal/domain/`) | 1 | 3 | 4 | 3 | 11 |
| App (`internal/app/`) | 1 | 5 | 7 | 8 | 21 |
| Adapters (`internal/adapters/`) | 2 | 4 | 5 | 4 | 15 |
| Cmd (`cmd/wa/`, `cmd/wad/`) | 0 | 5 | 11 | 11 | 27 |
| Project-wide | 0 | 0 | 1 | 0 | 1 |
| **Total** | **4** | **17** | **28** | **26** | **75** |

---

## Critical Issues (4)

### C-001: EventBridge mutex deadlock risk
- **File**: `internal/app/eventbridge.go:93-105`
- **Description**: Mutex held across external channel send operations in `Run()`. The code locks `b.mu` then performs non-blocking sends to multiple `w.ch` channels. If any waiter's cancel function tries to modify `b.waiters`, a deadlock can occur since the cancel func also acquires `b.mu`.
- **Fix**: Release the mutex before the inner loop, copy the waiter slice under lock, or redesign with channel-based waiter management.

### C-002: God struct — whatsmeow Adapter (13 fields)
- **File**: `internal/adapters/secondary/whatsmeow/adapter.go:82`
- **Description**: The `Adapter` struct has 13 fields (client, session, history, allowlist, auditBuf, logger, clientCtx, clientCancel, plus test-specific seed fields). Violates Single Responsibility Principle. Complex initialization, poor testability, tight coupling.
- **Fix**: Split into focused sub-structs (e.g., `sessionManager`, `eventBridge`, `auditWriter`) composed by the adapter.

### C-003: God struct — socket Server (13+ fields)
- **File**: `internal/adapters/primary/socket/server.go:36`
- **Description**: The `Server` struct has 13+ fields managing socket lifecycle, connection tracking, shutdown coordination, and configuration. Too many concerns bundled together.
- **Fix**: Extract shutdown orchestrator, connection registry, and configuration into separate types.

### C-004: Allowlist Grant() zero-value edge case
- **File**: `internal/domain/allowlist.go:92-105`
- **Description**: `Grant()` retrieves `a.entries[jid]` without checking existence. For a new JID, returns zero `actionSet` which is then modified. The check `if set.empty()` returns early, meaning a Grant on a previously empty entry may not persist correctly.
- **Fix**: Add explicit existence check and document the zero-value contract.

---

## High Severity Issues (17)

### H-001: Linear search in Group methods
- **File**: `internal/domain/group.go:40-47, 50-57`
- **Description**: `HasParticipant()` and `IsAdmin()` perform O(n) linear searches. With large groups (256+ participants), this is inefficient. No caching or map-based lookups.

### H-002: Session.NewSession uses wrong error type
- **File**: `internal/domain/session.go:23-24`
- **Description**: `NewSession()` uses `ErrInvalidJID` for `deviceID == 0` validation. Should use `ErrInvalidDeviceID` or a more specific sentinel.

### H-003: Magic hex constant buried in function scope
- **File**: `internal/domain/audit.go:108`
- **Description**: `const hex = "0123456789abcdef"` embedded in function scope. Should be module-level constant.

### H-004: Magic number — 30000ms default wait timeout
- **File**: `internal/app/method_wait.go:31`
- **Description**: `timeoutMs = 30000` is a magic number not defined as a named constant.
- **Fix**: Extract to `const defaultWaitTimeoutMs = 30000`.

### H-005: Magic number — 64 event buffer size
- **File**: `internal/app/eventbridge.go:52`
- **Description**: `make(chan Event, 64)` without explanation. Value should be named and documented.
- **Fix**: Extract to `const eventChannelBuffer = 64`.

### H-006: Magic number — 100ms retry backoff
- **File**: `internal/app/eventbridge.go:75`
- **Description**: `time.After(100 * time.Millisecond)` hardcoded without naming.
- **Fix**: Extract to `const eventStreamErrorBackoff = 100 * time.Millisecond`.

### H-007: Magic string — time format "15:04"
- **File**: `internal/app/ratelimiter.go:160`
- **Description**: Time format string "15:04" is hardcoded. Should be a named constant.

### H-008: Non-idiomatic error check (`if err == nil`)
- **File**: `internal/app/method_send.go:154`
- **Description**: Uses `if err == nil` instead of early-return `if err != nil` pattern. Violates Go's line-of-sight rule.

### H-009: Silent error swallowing — listener close
- **File**: `internal/adapters/primary/socket/server.go:130`
- **Description**: `_ = s.listener.Close()` silently discards errors during shutdown. Should log non-nil errors.

### H-010: Silent error swallowing — socket removal
- **File**: `internal/adapters/primary/socket/server.go:163`
- **Description**: `_ = os.Remove(s.path)` silently ignores filesystem errors.

### H-011: Silent error swallowing — SQLite cleanup
- **File**: `internal/adapters/secondary/sqlitehistory/store.go:75,85-106,152,202,300`
- **Description**: Multiple `defer func() { _ = rows.Close() }()` and `_ = db.Close()`. Database cleanup errors silently discarded.

### H-012: Potential race condition — Connection.subscriptions
- **File**: `internal/adapters/primary/socket/connection.go:27`
- **Description**: `subscriptions` map protected by `mu`, but concurrent access patterns exist: modified in `handleSubscribe`/`handleUnsubscribe` (holds `mu`) but read in `fanOutEvent` which may not hold the lock.

### H-013: Incomplete cleanup chain on error
- **File**: `cmd/wad/main.go:89-110`
- **Description**: Multiple cleanup paths in sessionStore/historyStore/auditLog error handling are duplicated. Each error handler manually closes previous resources, violating DRY. If one `Close()` fails mid-chain, subsequent resources may not be freed.

### H-014: Complex event loop with high cyclomatic complexity
- **File**: `cmd/wad/allowlist.go:118`
- **Description**: `watchAllowlist()` marked with `//nolint:gocyclo`. Nested select, timer, and signal handling. Debounce timer reset pattern (lines 170-176) is error-prone.

### H-015: Partial operation without atomic guarantee
- **File**: `cmd/wad/migrate.go:495-507`
- **Description**: `renamePlan()` applies renames sequentially but only rolls back on first failure. Later renames may be committed while migration marker is deleted.

### H-016: Hardcoded PATH in launchd plist
- **File**: `cmd/wad/service_darwin.go:80`
- **Description**: `/usr/local/bin:/usr/bin:/bin:/opt/homebrew/bin` hardcoded. Doesn't include `~/.local/bin` or other user-local paths.

### H-017: Timer not properly cleaned up on loop exit
- **File**: `cmd/wad/allowlist.go:135-138`
- **Description**: `debounce` timer created once but never explicitly stopped. If context cancels between Select and Reset, timer leaks until it fires.

---

## Medium Severity Issues (28)

### M-001: Inconsistent IsValid() implementations
- **Files**: `internal/domain/action.go:34-41` vs `internal/domain/event.go:30-32, 59-61`
- **Description**: Action uses switch with combined cases, Event uses boolean OR chains. Inconsistent patterns for the same concept.

### M-002: Hand-rolled JSON serialization
- **File**: `internal/domain/audit.go:71-89`
- **Description**: Comment admits avoiding `encoding/json` import. Custom JSON builder could have edge cases with special characters.

### M-003: Wrong error type for media path validation
- **File**: `internal/domain/message.go:70`
- **Description**: `MediaMessage.Validate()` returns `ErrEmptyBody` for empty path. Should be `ErrInvalidMessage` or a dedicated error.

### M-004: Complex inline boolean logic in Contact.IsZero()
- **File**: `internal/domain/contact.go:23`
- **Description**: Compound boolean `c.JID.IsZero() && c.PushName == "" && !c.Verified` could miss partially-zero contacts.

### M-005: Nested if statements (3 levels) in rate limiter
- **File**: `internal/app/ratelimiter.go:164-170`
- **Description**: Three levels of nesting in `checkRecipientLimits`. New-recipient check should be extracted into a helper method.

### M-006: Closure captured variable in loop
- **File**: `internal/app/eventbridge.go:130-138`
- **Description**: Cancel function captures `w` from loop scope. Pattern is error-prone and should be documented.

### M-007: Silent error classification in EventBridge
- **File**: `internal/app/eventbridge.go:72`
- **Description**: When `EventStream.Next()` returns an error, bridge logs and retries without distinguishing transient from fatal errors.

### M-008: God struct — Dispatcher (15 fields, 16 methods)
- **File**: `internal/app/dispatcher.go:36-52`
- **Description**: Holds 15 fields and 16 methods across 5 files. Consider splitting into focused handler groups.

### M-009: Test helper missing t.Helper()
- **File**: `internal/app/dispatcher_test.go:17`
- **Description**: `newTestDispatcher` helper missing `t.Helper()` call, making test output less clear.

### M-010: Inconsistent test cleanup pattern
- **Files**: `internal/app/method_send_test.go:33`, `internal/app/dispatcher_test.go:327`
- **Description**: Some tests use `t.Cleanup(func() { _ = d.Close() })` with error ignored.

### M-011: Non-idiomatic lock release pattern
- **File**: `internal/adapters/primary/socket/subscribe.go:94`
- **Description**: Early `Unlock()` on error path instead of defer-based unlock with recovery.

### M-012: Long function — Server.Run() (~90 lines)
- **File**: `internal/adapters/primary/socket/server.go:87`
- **Description**: Complex shutdown choreography spanning ~90 lines. Hard to test shutdown scenarios in isolation.

### M-013: Complex shutdown cleanup should be extracted
- **File**: `internal/adapters/primary/socket/server.go:130-175`
- **Description**: Close listener, send notifications, close reads, drain, remove socket — should be helper methods.

### M-014: Complex SQLite error handling
- **File**: `internal/adapters/secondary/sqlitehistory/store.go`
- **Description**: Multiple nested defers and error checks create complex control flow. `Open()` has 10+ error paths.

### M-015: Incomplete sqlitestore adapter
- **File**: `internal/adapters/secondary/sqlitestore/`
- **Description**: Only `store.go` and `doc.go` present. Missing adapter implementation file.

### M-016: Mixed validation levels in profile probe
- **File**: `cmd/wa/cmd_profile.go:78-93`
- **Description**: `probeProfile()` reads raw filesystem entries without validation but processes them. Returns "invalid" for failed `ValidateProfileName` but still tries to stat them.

### M-017: Manual flag parsing with off-by-one risk
- **File**: `cmd/wad/service.go:48-60`
- **Description**: `parseServiceProfileFlag()` uses manual `os.Args` parsing. Silently returns DefaultProfile for malformed invocations.

### M-018: Silent EOF handling in atomic write
- **File**: `cmd/wa/cmd_profile.go:41`
- **Description**: `atomicWriteClient()` falls back to DefaultProfile silently if active-profile file is unreadable. Users won't know about corruption.

### M-019: Off-by-one in flag parsing (confusing bounds check)
- **File**: `cmd/wad/profile_resolve.go:25-27`
- **Description**: `i+1 < len(os.Args)-1` is equivalent to `i+2 < len(os.Args)` but harder to read. The `os.Args[i+2]` access is safe but confusing.

### M-020: Magic environment variable parsing without bounds
- **File**: `cmd/wad/main.go:213-220`
- **Description**: `WA_RETENTION_DAYS` parsed with `fmt.Sscanf` with no upper bounds (could be 1,000,000 days). No log message if malformed.

### M-021: os.Exit in command handlers breaks testability
- **Files**: `cmd/wa/cmd_profile.go:227-233, 240-242`, `cmd/wa/cmd_allow.go:27`, `cmd/wa/cmd_history.go:25`
- **Description**: `os.Exit(64)` / `os.Exit(78)` called directly in command handlers. Should return errors and let main() decide exit codes.

### M-022: Error swallowed in allowlist reload
- **File**: `cmd/wad/allowlist.go:140-155`
- **Description**: Parse error during reload logs but silently keeps old state. No metric/counter incremented.

### M-023: Composition root exceeds gocyclo threshold
- **File**: `cmd/wad/main.go:41-253`
- **Description**: Function has 25 sequential steps with `//nolint:gocyclo`. Hard to refactor due to resource lifecycle ordering but test-unfriendly.

### M-024: Dispatcher configuration scattered
- **File**: `cmd/wad/main.go:152-177`
- **Description**: Dispatcher creation, history method registration, and handler setup interleaved. Should consolidate into a single sub-function.

### M-025: XDG_RUNTIME_DIR template substitution
- **File**: `cmd/wad/service_linux.go:58`
- **Description**: `XDG_RUNTIME_DIR=/run/user/%U` hardcodes the path. systemd `%U` is the numeric UID, not username.

### M-026: Error wrapping inconsistency — `%w` + `%v` pattern
- **Files**: `cmd/wad/runtime_dir.go:36,58`, `internal/adapters/primary/socket/lock.go:35,41`, 10+ other files
- **Description**: Multiple files use `fmt.Errorf("%w: context %v", sentinel, err)` which adds extra `%v` verb alongside `%w`. Best practice is `fmt.Errorf("context: %w", err)` without mixing verbs.

### M-027: RegisterWaiter filter parameter not validated
- **File**: `internal/app/eventbridge.go:117`
- **Description**: `filter` parameter accepts any `[]string` without validating that event type strings exist in the domain. Invalid names silently receive no events.

### M-028: Dry-run purge gives no feedback on deletion size
- **File**: `cmd/wa/cmd_purge.go:33`
- **Description**: `_ = result` — the history query result is fetched but completely ignored. Dry-run should count messages but doesn't.

---

## Low Severity Issues (26)

### L-001: Magic number for group subject limit
- **File**: `internal/domain/group.go:6`
- **Description**: `const maxGroupSubjectBytes = 100` lacks explanation. Should reference WhatsApp protocol limit.

### L-002: Magic numbers for phone validation
- **File**: `internal/domain/jid.go:72-73`
- **Description**: `len(digits) < 8 || len(digits) > 15` are ITU-T E.164 bounds but should be named constants.

### L-003: Sentinel enum documentation
- **File**: `internal/domain/event.go:8-13`
- **Description**: Zero-invalid `iota + 1` pattern could be documented as defensive against accidental zero values.

### L-004: Allowlist Grant/Revoke asymmetry
- **File**: `internal/domain/allowlist.go:95-104`
- **Description**: `Grant()` modifies and stores but doesn't remove empty entries. `Revoke()` explicitly deletes. Inconsistent behavior.

### L-005: Waiter slice linear lookup O(n)
- **File**: `internal/app/eventbridge.go:133-137`
- **Description**: Removing a waiter uses linear search. Use a map if performance becomes critical.

### L-006: No benchmarks for hot paths
- **File**: `internal/app/`
- **Description**: No benchmarks for `RateLimiter.Allow()` or `RateLimiter.AllowFor()`.

### L-007: Incomplete error context in method_send
- **Files**: `internal/app/method_send.go:65, 104, 142`
- **Description**: `fmt.Errorf("send: %w", err)` adds minimal context.

### L-008: No rate limiter day-boundary reset test
- **File**: `internal/app/ratelimiter_test.go`
- **Description**: Tests don't verify the midnight reset logic in `checkRecipientLimits` (lines 148-152).

### L-009: Anonymous struct definitions in tests
- **Files**: `internal/app/method_send_test.go:49-51`, `internal/app/dispatcher_test.go:134-137`
- **Description**: Anonymous structs for JSON unmarshaling should be extracted to package-level types for reuse.

### L-010: Unused test variable pattern
- **File**: `internal/app/dispatcher_test.go:327-390`
- **Description**: Test cases discard return values with `_` unnecessarily.

### L-011: Ignored tabwriter errors
- **Files**: `cmd/wa/cmd_allow.go:105,107,109`, `cmd/wa/cmd_groups.go:46-50`, `cmd/wa/cmd_history.go:76-89`
- **Description**: `_, _ = fmt.Fprintf()` and `_ = w.Flush()` ignore all CLI output errors.

### L-012: Hardcoded exit codes without symbolic constants
- **File**: `cmd/wad/migrate_cmd.go:33, 43, 49, 59`
- **Description**: Exit codes 64 hardcoded throughout. Should use the exitcodes pattern from `cmd/wa`.

### L-013: Magic number for truncation
- **Files**: `cmd/wa/cmd_history.go:84-85`, `cmd/wa/cmd_search.go`
- **Description**: `body[:77] + "..."` truncates to 80 chars with no named constant.

### L-014: Unidiomatic error handling in formatHuman
- **File**: `cmd/wa/output.go:39`
- **Description**: JSON unmarshal error is silently caught and falls through to switch. Default case may also fail silently.

### L-015: json.Marshal error ignored
- **Files**: `cmd/wa/cmd_history.go:34`, `cmd/wa/cmd_search.go:24`, `cmd/wa/cmd_export.go:21`, `cmd/wa/cmd_messages.go:17`
- **Description**: `raw, _ := json.Marshal(params)` — error discarded. Marshal of simple maps shouldn't fail but convention is to check.

### L-016: Unmarshal error ignored in purge
- **File**: `cmd/wa/cmd_purge.go:48`
- **Description**: `_ = json.Unmarshal(result, &resp)` — error ignored.

### L-017: Defensive nolint annotations in migrate
- **File**: `cmd/wad/migrate.go:82, 97, 107`
- **Description**: Multiple `//nolint:gosec // G304` annotations. Justified but suggests code is hard to understand for future developers.

### L-018: Profile regex validation duplicated
- **File**: `cmd/wa/profile.go:7-11` and corresponding `cmd/wad` code
- **Description**: Regex and reserved names list duplicated. Should be in shared internal package.

### L-019: Global flag registration in init()
- **File**: `cmd/wa/root.go:64-103`
- **Description**: CLI flags registered in `init()` via global cobra.Command mutation. Standard Cobra pattern but makes testing harder.

### L-020: Signal context with no explicit cancel on panic
- **File**: `cmd/wad/main.go:195-196`
- **Description**: Signal context's `stop()` is deferred. If panic occurs before defer runs, signal handling persists. Not a real issue (process exits) but could be more defensive.

### L-021: Missing error logging on connection close
- **File**: `cmd/wa/rpc.go:95`
- **Description**: `defer func() { _ = conn.Close() }()` — connection close error intentionally ignored with no logging.

### L-022: Legitimate contract violation panics (informational)
- **Files**: `internal/adapters/secondary/whatsmeow/translate_jid.go:39,43`, `internal/adapters/secondary/whatsmeow/audit.go:33`
- **Description**: All panics are legitimate contract enforcement with `//nolint:forbidigo` justifications. **No action needed.**

### L-023: No test for EventBridge filter validation
- **File**: `internal/app/eventbridge.go:117`
- **Description**: Invalid event names in filter silently receive no events. No test verifies this contract.

### L-024: Connection close error in test cleanup
- **File**: `internal/app/method_send_test.go:33`
- **Description**: `t.Cleanup(func() { _ = d.Close() })` with error discarded.

### L-025: Missing benchmarks for socket hot paths
- **File**: `internal/adapters/primary/socket/`
- **Description**: No benchmarks for connection notification throughput or event fan-out.

### L-026: Incomplete adapter — sqlitestore
- **File**: `internal/adapters/secondary/sqlitestore/`
- **Description**: Only `store.go` and `doc.go` present. May be intentional placeholder.

---

## Positive Findings

The audit also surfaced significant strengths worth preserving:

- **No nested if statements exceeding 2 levels** in domain layer
- **No functions exceeding 40 lines** in domain layer
- **Excellent error handling** with sentinel errors and proper `errors.Is`/`errors.As` usage
- **Proper sealed interfaces** (Event, Message sum types)
- **Exemplary import organization** — consistent stdlib/external/internal grouping
- **Strong architectural enforcement** via `depguard` in `.golangci.yml`
- **No domain type leakage** — whatsmeow types properly translated at boundaries
- **Comprehensive test coverage** — 56 test files, shared `porttest/` contract suite
- **Proper context management** — dual-context pattern (client vs request) correctly implemented
- **Proper shutdown choreography** — multi-phase drain with per-component timeouts
- **All panics justified** with `//nolint:forbidigo` documentation
