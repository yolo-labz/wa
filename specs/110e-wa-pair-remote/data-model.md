# Feature 110e — Data model

Single CLI-only value type. No persistent state, no DB schema, no migration.

## Types

### `RemoteTarget`

```go
// RemoteTarget names a remote daemon by its SSH host and dokku app.
// Lives in package `main` under cmd/wa. Not a domain or app type —
// pair-remote is a CLI-layer concern (FR-007: zero daemon-side change).
type RemoteTarget struct {
    Host string // SSH destination — anything `ssh` resolves (hostname, ~/.ssh/config alias, user@host, Tailscale name).
    App  string // dokku app name (e.g. "wa-burocracy"). Used in `dokku enter <App>`.
}
```

**Invariants** (enforced by `ParseRemoteTarget`, not by struct construction — Go has no "smart constructor" syntax):

| Invariant | Enforced where |
|---|---|
| `Host` is non-empty | `ParseRemoteTarget` returns `errInvalidRemote` if empty after split. |
| `App` is non-empty | same |
| Input is not a URL form (`http://`, `https://`) | `ParseRemoteTarget` returns `errURLNotAccepted` with the spec-mandated message. |
| Input contains exactly one `:` separator (split on FIRST `:`) | `strings.Cut(s, ":")` returns `(host, app, true)` on a single colon; multi-colon inputs treat everything after first colon as `App`. Tested edge case. |

## Functions

### `ParseRemoteTarget`

```go
func ParseRemoteTarget(s string) (RemoteTarget, error)
```

**Behaviour**:

| Input | Output |
|---|---|
| `"ProxMox.Dokku:wa-burocracy"` | `RemoteTarget{Host: "ProxMox.Dokku", App: "wa-burocracy"}, nil` |
| `"pedro@host.example:wa-personal"` | `RemoteTarget{Host: "pedro@host.example", App: "wa-personal"}, nil` |
| `"host:app:extra"` | `RemoteTarget{Host: "host", App: "app:extra"}, nil` — second colon is part of App. (`strings.Cut` semantics.) |
| `""` | `RemoteTarget{}, errInvalidRemote("expected <host>:<app>, got empty string")` |
| `"no-colon"` | `RemoteTarget{}, errInvalidRemote("expected <host>:<app>, got missing ':' separator")` |
| `":app"` | `RemoteTarget{}, errInvalidRemote("expected <host>:<app>, got empty host")` |
| `"host:"` | `RemoteTarget{}, errInvalidRemote("expected <host>:<app>, got empty app")` |
| `"https://wa.example.com"` | `RemoteTarget{}, errURLNotAccepted(...)` — the spec-mandated FR-003 message. |
| `"http://wa.example.com"` | same as above. |

### Error types

```go
type remoteParseError struct {
    Code    int    // 64 (sysexits EX_USAGE) per FR-002 / FR-003
    Message string // operator-facing
}

func (e remoteParseError) Error() string { return e.Message }
func (e remoteParseError) ExitCode() int { return e.Code }
```

Exit-code mapping reuses the existing `exiterr` helper in `cmd/wa/output.go`. The parser does not handle exit itself — the caller (`runPairRemote` or its harness) calls `exiterr(err.ExitCode(), err)`.

## Relationships

```
cmd_pair.go ────────────────────────▶  cmd_pair_remote.go
   (cobra command body,                  (parser + exec helper)
    short-circuits to                     ── ParseRemoteTarget(...)
    runPairRemote when                    ── runPairRemote(target, extraFlags)
    --remote != "")                       ── execCommand var (test seam)
```

No relationship with `internal/` packages. No relationship with `domain` types.

## State transitions

None. `RemoteTarget` is immutable after construction.

## Validation rules

All in `ParseRemoteTarget`. No runtime mutation of an already-parsed `RemoteTarget`. No cross-field invariants beyond the two non-empty checks.

## Migration

None. New type, new file, no schema change.

## Test coverage matrix

| Case | Test name |
|---|---|
| Valid host + app | `TestParseRemoteTarget_HappyPath` |
| Valid `user@host` form | `TestParseRemoteTarget_UserAtHost` |
| Empty input | `TestParseRemoteTarget_EmptyRejected` |
| Missing `:` | `TestParseRemoteTarget_MissingSeparator` |
| Empty host | `TestParseRemoteTarget_EmptyHost` |
| Empty app | `TestParseRemoteTarget_EmptyApp` |
| `https://` URL form | `TestParseRemoteTarget_URLRejected` |
| `http://` URL form | `TestParseRemoteTarget_URLRejected` (table-driven, same test) |
| Multi-colon app name | `TestParseRemoteTarget_MultiColonApp` |
