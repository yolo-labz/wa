# Feature 110d — sqlite tokens store + `wad token` admin

**Branch**: `110d-tokens-store`
**Status**: implemented
**Source**: spec 110 fourth sub-PR. 110a single-env-var bearer
token; 110b SSE events; 110c client-side `wa --remote`. 110d
replaces the env-var with a sqlite-backed multi-token store + per-
method scope enforcement + `wad token issue|revoke|list|sweep`
admin subcommands.

## Problem

110a's env-var token is a one-token, no-revocation, all-admin
surface. Multi-host deploys need per-machine credentials, scoped
permissions (read-only agents, send-only operators, admin), and
revocation without rolling the whole secret.

## Decision

New secondary adapter `internal/adapters/secondary/sqlitetokens/`
manages a `tokens.db` SQLite file, one row per issued token:

```
token_id    TEXT PRIMARY KEY  -- ULID
hash        BLOB UNIQUE       -- sha256(raw token), never plaintext
name        TEXT              -- operator label
scope       TEXT              -- read | send | admin
created_at  INTEGER           -- unix seconds
expires_at  INTEGER           -- unix seconds, 0 = never
last_used_at INTEGER          -- buffered writer (60s flush)
revoked_at  INTEGER           -- nullable; non-null = revoked
```

The REST adapter gains a new `TokenStore` interface and a scoped
`Authenticator` that delegates to it. When `WAD_REST_TOKEN_DB` is
set, `cmd/wad/rest_http.go` opens the store and wires
`rest.NewScopedAuth` instead of the env-var `EnvTokenAuth`. The
env-var path remains as legacy backwards-compat (single-token,
implicit admin scope).

`wad token issue|revoke|list|sweep` operates directly on
`tokens.db` without starting the daemon — the db file is the source
of truth and locking is sqlite WAL-mode safe.

### Per-method scope table

`internal/adapters/primary/rest/scope.go` declares a
`MethodScopes` map. Tokens with `granted >= required` pass; unknown
methods fail closed. Three coarse levels:

- **read** — status, messages, history, search, contacts.*, media.*,
  draft.list/get, groups, groups.get, embeddings.status,
  privacy.get
- **send** — read + send/sendMedia/react/markRead, draft.approve/
  reject, message.forward/star, presence.*, contacts.annotate/sync,
  poll.vote, wait, media.gc
- **admin** — send + pair, panic, allow, contact.block/unblock,
  chat.*, profile.*, group.*, message.revoke/edit, schedule.*,
  labels.*, embeddings.purge, privacy.set, session.logoutAll, admin.*

### Activation matrix

| `WAD_REST_HTTP_ADDR` | `WAD_REST_TOKEN_DB` | `WAD_REST_TOKEN` | Behaviour |
|----------------------|----------------------|-------------------|-----------|
| unset                | (any)                | (any)             | no listener |
| set                  | set                  | (any)             | sqlite scoped store; env-var ignored |
| set                  | unset                | set               | env-var legacy single-token (implicit admin) |
| set                  | unset                | unset             | **REFUSE TO START** |

### `wad token` subcommands

- `wad token issue --name LABEL --scope read|send|admin [--ttl 30d] [--db PATH]`
  — mints a fresh 256-bit token, prints the JSON envelope (operator
  captures `token` field once, never recoverable later).
- `wad token revoke --id TOKEN_ID [--db PATH]` — sets `revoked_at`.
  Idempotent.
- `wad token list [--db PATH] [--json]` — newest-first table.
- `wad token sweep [--older-than 168h] [--db PATH]` — deletes
  inactive rows whose `revoked_at` or `expires_at` is older than
  cutoff.

## Alternatives rejected

Per Constitution rule 20.

### A. Continue with env-var token; expose `wad token` over the daemon socket

Skip the sqlite store; let operators pass multiple tokens via env
or admin RPC. **Rejected** because:

1. Multi-token state needs durable storage. Env vars don't survive
   restart cleanly without a separate config-management layer.
2. `wad token` over the daemon socket couples token admin to a
   running daemon — useless when bootstrapping or recovering from
   a wedged session.

### B. JWT instead of opaque tokens + sqlite

Mint signed JWTs the daemon validates statelessly. **Rejected**
because Codex review on PR 110a already documented the JWT
alg-confusion class of bugs + the revocation-still-needs-a-denylist
trap. Single-tenant daemon: opaque + sqlite is simpler and safer.

### C. PASETO v4.local instead of opaque tokens

PASETO removes the alg-confusion footgun. **Rejected** because:

1. Same revocation requirement as JWT.
2. Opaque + sqlite needs zero key management; PASETO needs a
   shared secret.
3. The win (offline verification across multiple verifiers) doesn't
   apply — single daemon, one verifier.

## Functional requirements

- **FR-001** — `Issue(name, scope, ttl)` returns a fresh 256-bit
  token (32 random bytes hex-encoded = 64 chars). Raw is the only
  source of the secret; verify any future call against
  sha256(raw).
- **FR-002** — `Verify(rawToken)` returns the token row OR a
  sentinel error: `ErrTokenNotFound`, `ErrTokenRevoked`,
  `ErrTokenExpired`. Constant-time compare on the hash.
- **FR-003** — `Revoke(id)` is idempotent — re-revoking returns nil
  without changing state.
- **FR-004** — `List()` returns every token row newest-first by
  `created_at`. Revoked tokens still appear with `RevokedAt`
  populated.
- **FR-005** — `Sweep(olderThan)` deletes inactive rows older than
  cutoff. Active rows survive.
- **FR-006** — REST `Authenticator` enforces per-method scope.
  `tokenScope >= MethodScopes[method]`; unknown methods fail
  closed (HTTP 403, `error.code=-32099`).
- **FR-007** — `cmd/wad/rest_http.go` selects the store backend by
  env var precedence: `WAD_REST_TOKEN_DB` over `WAD_REST_TOKEN`.
  Both unset with `WAD_REST_HTTP_ADDR` set → REFUSE TO START.
- **FR-008** — `wad token` subcommands operate directly on the
  `tokens.db` file via `--db PATH` flag (or `$WAD_REST_TOKEN_DB`
  env). The daemon does NOT need to be running.

## Tests

`internal/adapters/secondary/sqlitetokens/store_test.go`:
- `TestIssue_HappyPath` pins token format + TTL semantics
- `TestIssue_InvalidScope` pins fail-closed scope validation
- `TestIssue_BlankNameRejected` pins the empty-name guard
- `TestVerify_RoundTrip` pins issue → verify
- `TestVerify_UnknownToken` → ErrTokenNotFound
- `TestVerify_Expired` → ErrTokenExpired
- `TestRevoke_Idempotent` pins double-revoke is nil + verify after
  → ErrTokenRevoked
- `TestRevoke_Unknown` → ErrTokenNotFound
- `TestList_NewestFirst` pins ordering + revoked-still-listed
- `TestSweep` pins inactive rows deleted, active survive
- `TestVerify_LastUsedAtBuffered` pins flush updates last_used_at

`internal/adapters/primary/rest/scoped_auth_test.go`:
- `TestScopedAuth_AdminPasses` — admin reaches send method
- `TestScopedAuth_ReadCannotSend` — 403
- `TestScopedAuth_SendCannotAdmin` — 403
- `TestScopedAuth_ReadReachesStatus` — 200
- `TestScopedAuth_UnknownMethodFailsClosed` — 403 even with admin
- `TestAllowedScope_Table` — direct unit table

## Out of scope

- **Token rotation cadence** (auto-renew before expiry).
- **Refresh tokens** — single-use opaque tokens are simpler.
- **OAuth2/OIDC integration**.
- **Per-method allowlist beyond the three coarse scopes**.
- **`wa auth login|logout|status` keychain integration on the
  client side** — operator stores token in `WA_TOKEN` env via
  shell rc.

## References

- Spec 109 — Dokku container + SSH-forward CLI
- Spec 110 — REST adapter parent design (rejected JWT/PASETO/mTLS
  in favour of opaque + sqlite, this PR makes the choice concrete)
- Spec 110a — REST adapter skeleton (env-var token retained as
  legacy backwards-compat path)
- Spec 110c — `wa --remote` CLI mode (consumes any token issued
  by 110d transparently — token is just bytes on the wire)
