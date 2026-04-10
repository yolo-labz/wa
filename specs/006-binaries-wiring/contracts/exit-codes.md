# Contract: Exit Codes

**Feature**: 006-binaries-wiring

The `wa` CLI MUST return sysexits-conformant exit codes per CLAUDE.md §Output schema.

## Table

| Exit code | Name | JSON-RPC error code(s) | When |
|---|---|---|---|
| 0 | OK | (success response) | Command succeeded |
| 1 | Generic error | any unlisted code | Unexpected error |
| 10 | Service unavailable | `-32011` (NotPaired), connection refused/reset | Daemon not running or not paired |
| 11 | Not allowlisted | `-32012` (NotAllowlisted) | Target JID not on allowlist |
| 12 | Rate limited / timeout | `-32013` (RateLimited), `-32014` (WarmupActive), `-32003` (WaitTimeout) | Rate/warmup cap exceeded or wait timed out |
| 64 | Usage error | `-32015` (InvalidJID), `-32016` (MessageTooLarge), `-32602` (InvalidParams), `-32601` (MethodNotFound) | Client-side input error |
| 78 | Config error | (startup, before RPC) | Socket path invalid, config file corrupt |

## Implementation

`cmd/wa/exitcodes.go` defines a `func rpcCodeToExit(code int) int` table lookup.

## Test coverage

Every exit code MUST have at least one testscript `.txtar` covering it.
