# Feature 116 — account admin web UI + agent-safe MCP

**Branch**: `116-account-admin-mcp` (design-only — no code in this PR)
**Status**: drafted 06/11/2026; `plan.md` / `tasks.md` follow in their own PRs
**Source**: Pedro, 06/11/2026 — "We need a webui in order to add or administrate the
connected accounts and we need a really good MCP to avoid issues like we are having on
the Household & Supply workspace."
**Builds on**: 110 (REST adapter), 110d (token store), 110e (`wa pair --remote`),
110g (soft-stale), 111 (MCP adapter), the #312 error-wire table.
**Verified against**: `main` at `cc313dc` (06/11/2026).

This spec locks decisions so that implementation PRs execute without re-deciding,
following the 110/111 precedent. Every claim about the code cites `file:line` on
`cc313dc` (CLAUDE.md R11). Failure classes come from a sanitised list of what the
Household agents hit; this document carries no account identifiers, numbers, names or
message text, and none may be added to it.

## 1. Problem

Seven failure classes each cost an agent a slice or produced a false diagnosis. Each one
was re-checked on `cc313dc` before any requirement below was written (R29: cite the
code, not the anecdote). A class that had been fixed would be dropped from scope here;
none was fixed outright, though four have parts that are already closed, recorded as such.

### F1 — a missing message reads as something else

*Observed 31/07.* Media fetch by a message id that did not exist returned
`-32603 internal error`. The agent concluded "media expired" and spent a session on it.

**Today.** The socket/REST half is fixed. `loadAdvertisedProto` maps a missing row to
`app.ErrMessageNotFound` (`-32117`), and its comment records this exact incident
(`internal/adapters/secondary/whatsmeow/adapter_media.go:130-139`). The shared wire
table `app.RPCWire` keeps typed sentinels from collapsing to `-32603` on both transports
(`internal/app/rpcwire.go:53`, commit `dfd87cf`, #312).

The MCP half is **not** fixed, and it is now worse than a bare `-32603`:

- `mcpbridge.remediate` classifies by numeric band only. Every code in
  `-32199..-32100` gets "the recipient is not allowlisted… `wa allow add`"
  (`internal/mcpbridge/errors.go:12-15`). `-32117 message not found` falls in that band,
  so `wa_transcribe_voice` on a bad id tells the agent the **recipient is not
  allowlisted**. Measured with a throwaway scratch test against `cc313dc` (not
  committed):
  `-32117 -> policy refusal (-32117): message not found. The recipient is not allowlisted for this action; …`
- The Streamable HTTP transport does not use `RPCWire` at all. `dispatcherCaller` does
  `errors.As(err, &coder)` and defaults to `-32603` (`cmd/wad/mcp_http.go:43-50`), which
  repeats the exact pre-#312 bug: plain sentinels (`domain.ErrMediaNotCached`,
  `domain.ErrMediaUnsupported`, `domain.ErrBlocked`, …) carry no `RPCCode` and arrive as
  `-32603 internal error`. It also replaces every message with `"rpc error"`
  (`cmd/wad/mcp_http.go:47-50`), so the agent cannot tell which input was rejected.
- `-32301 MediaNotCached` and `-32300 UnsupportedMessageType` fall to the
  `default` branch, `daemon error (-32301): …`, with no remediation
  (`internal/mcpbridge/errors.go:26-27`).

Verified on main: internal/mcpbridge/errors.go:12 — still present (MCP path; the socket/REST path was fixed in dfd87cf)

### F2 — "not allowlisted" with no pre-flight and no way out

*Observed 31/07.* A send was refused with `-32012 not allowlisted`. The only remedy was
operator-side: issue an admin token inside the container, run `allow add` remotely, then
revoke the token. `wad allow list` / `wad --help` inside the container booted a second
full daemon (the EXIT 124 trap). The agent had no pre-flight check and the error carried
no hint.

**Today.**

- *Fixed:* the EXIT 124 trap. `classifyDaemonArgs` refuses unknown arguments and prints
  usage for `--help` before any store opens (`cmd/wad/args.go:61-93`, commit `7c1a5dc`,
  #374).
- *Still present:* no pre-flight. The only allowlist read is the `allow` method with
  `op:"list"` (`cmd/wad/methods.go:47-48`), which is classified `ScopeAdmin` as a whole
  method (`internal/adapters/primary/rest/scope.go:115`). A read or send token cannot ask
  "may I send to X?" before trying.
- *Still present:* no remediation on the wire. Socket/REST answer the constant
  `-32100 "PolicyRefused"` (`internal/app/rpcwire.go:70-74`). The constant body is
  deliberate (FR-050: no JID probing), so the fix must be a constant remediation, not a
  detailed one.
- *Still present, MCP-specific:* over `/mcp`, `dispatcherCaller` keeps the sentinel's own
  `-32012` (`cmd/wad/mcp_http.go:44-46`). `remediate` puts `-32012` in the upstream band
  and tells the agent "this is a protocol-side failure, **not a policy refusal**"
  (`internal/mcpbridge/errors.go:23-25`), which is the opposite of the truth. The same
  scratch run shows `-32011 not paired` and `-32012 not allowlisted` both receiving the
  "upstream error" text.

Verified on main: internal/adapters/primary/rest/scope.go:115 — still present (the EXIT 124 sub-class was fixed in 7c1a5dc)

### F3 — scope learned only after the damage

*Observed 24/09.* `message.edit` with the deployed `scope=send` token answered
`403 scope insufficient`. The agent learned the required scope only after a send had gone
out wrong and could not be corrected.

**Today.** `message.edit` (and `message.revoke` without `scope:"self"`) are `ScopeAdmin`
(`internal/adapters/primary/rest/scope.go:137-138`, `scope.go:206-214`). The refusal is
`403`, code `-32099`, body `"scope insufficient for method"`
(`internal/adapters/primary/rest/server.go:345-349`). It names neither the required nor
the held scope, and `-32099` is the same code REST uses for "media not found", so code
alone cannot tell the two apart. No method lets a caller read its own scope or the scope a
method needs. On `/mcp`, send and admin tokens are served the same tool set
(`cmd/wad/mcp_http.go:89`, `:121`), and no tool advertises the scope it requires.

Verified on main: internal/adapters/primary/rest/server.go:347 — still present

### F4 — unpaired after a reboot, and no browser path back

*Observed 25/09.* After a host reboot the daemon came up `paired=false`. Re-pairing needs
the owner's phone plus an ssh/dokku idiom. There is no browser path.

**Today.** `wa pair --remote <ssh-host>:<app>` shipped (`cmd/wa/cmd_pair_remote.go`,
commit `ff43fe8`, #147), but it is the SSH chain wrapped in a flag. The command itself
refuses URL forms ("pair requires SSH access", `cmd/wa/cmd_pair.go:84`), so an operator
without shell access to the Dokku host still cannot pair. The REST listener has no pairing
page (`internal/adapters/primary/rest/server.go:185-217` lists every route).

Two related defects bear on how a browser path may be built:

- The QR flow prints each code as a half-block QR to the daemon's **stderr**
  (`internal/adapters/secondary/whatsmeow/pair.go:135`). In a container, stderr is the
  log stream, so a device-link credential lands in the platform's log store.
- The same flow writes the QR to a pair HTML file in the shared temp dir
  (`internal/adapters/secondary/whatsmeow/pair.go:139`). The file is SEC-03 hardened but
  outlives the pairing window.
- `pairhtml.Render` emits an inline `<style>` block and a `data:` image
  (`internal/pairhtml/pairhtml.go:50`, `:101`). Neither is allowed under
  `default-src 'self'`, so the template cannot be reused unchanged on a strict-CSP page.

Verified on main: cmd/wa/cmd_pair.go:84 — still present (the SSH-chain `--remote` UX shipped in ff43fe8 and does not remove the need for shell access)

### F5 — "healthy" while nothing is being stored

*Observed 06/11.* Health reported paired/connected while the host disk was 100 % full. New
messages were not persisted: a chat's history stopped two days before while new messages,
attachments included, were still arriving. Nothing said "degraded: storage".

**Today, and worse than reported.**

- `/readyz` calls `dispatcher.IsReady` (`cmd/wad/main.go:679`). Its comment says
  "paired AND connected" (`cmd/wad/health_http.go:52`), but `IsReady` returns
  `!sess.IsZero()`: it checks **session existence only**
  (`internal/app/method_health.go:127-136`). A disconnected or demoted daemon is "ready".
- `status`, which is what MCP `wa_status` calls (`internal/mcpbridge/tools.go:338-346`),
  sets `Connected = true` whenever a session row exists
  (`internal/app/method_status.go:48-50`). That is the transport-free green lie R26
  forbids. It also returns the full account JID to the agent.
- A persist failure is recorded only as an audit `panic` detail
  (`internal/adapters/secondary/whatsmeow/adapter_inbound.go:143-145`). The audit log is
  on the same full disk, and no counter or timestamp that health reads is updated.
- No code anywhere probes free space or writability (no `Statfs`, `ENOSPC` or
  writability check in `internal/` or `cmd/`). `health` exposes `lastEventTs`, the last
  event **received** (`internal/app/method_health.go:70`), never the last event
  **persisted**, so received-but-not-stored is invisible.

Verified on main: internal/app/method_health.go:135 — still present

### F6 — a long operation on a synchronous request

*Observed 06/11.* `sync force --chat … --count 50` over the remote endpoint gave HTTP 502
after about 30 s, with no job id to poll.

**Today.** Chat-scoped `sync.force` blocks until a matching on-demand response arrives or
the deadline passes (`internal/adapters/secondary/whatsmeow/sync.go:180-187`). The
deadline is `historyRequestTimeout = 30 * time.Second`
(`internal/adapters/secondary/whatsmeow/history.go:32`), which equals the REST server's
`WriteTimeout` of 30 s (`internal/adapters/primary/rest/server.go:231`). Any reverse proxy
with a timeout of 30 s or less kills the request first. The global form is already async
(`sync.go` `forceGlobalBackfill`, `Async=true`), but nothing returns a handle to poll.

The same class exists on the MCP surface: `wa_wait_for_reply` defaults to 60 s and allows
300 s (`internal/mcpbridge/tools.go:205-211`), while `/mcp` is served by the same
`http.Server` with the same 30 s `WriteTimeout`.

Verified on main: internal/adapters/secondary/whatsmeow/history.go:32 — still present

### F7 — ambiguous time units and a hidden caption

*Observed 02/11.* `wa export --json` writes `timestamp` in seconds with no unit in the
name. A consumer divided by 1000 and dated everything 1970. Captioned media had an empty
`body` with the caption inside `channel`.

**Today.** `wireMessage.Timestamp int64 json:"timestamp"` (unix seconds, unnamed unit) is
the shape for export, history and messages (`cmd/wad/history_methods.go:314`). The same
unnamed field is on `media.list` rows (`cmd/wad/media_list_method.go:93`). For inbound
rows, `body` and `caption` are deliberately left empty and every attacker-controllable
string is folded into the FR-005a `channel` envelope
(`cmd/wad/history_methods.go:394-398`). The envelope is a safety invariant (CLAUDE.md
§Safety 5), so the fix cannot be "put the raw caption back in `caption`". It must make
the caption's presence and position explicit without unwrapping untrusted text.

Verified on main: cmd/wad/history_methods.go:314 — still present

### F8 (adjacent, not a numbered class) — the MCP has no media read tools

The 12 registered tools (`internal/mcpbridge/tools.go:101-338`) include no media list or
fetch tool. `wa_transcribe_voice` is the only media path. Household agents therefore fell
back to the CLI to retrieve attachments. The daemon already has the right primitive:
`media.list` filters by chat, sender and caption, and reports `appliedFilters` so a
silently ignored filter is detectable (`cmd/wad/media_list_method.go:21-75`, #314, #317).

## 2. Accounts model

**What an account is today.** An account is one WhatsApp linked device, held by one
`wad` process:

- one `wad` process serves exactly one profile, and profiles share no in-process state
  (CLAUDE.md:228; profile names are validated at `cmd/wad/profile.go:30`);
- each process opens exactly one device: `container.GetFirstDevice`
  (`internal/adapters/secondary/whatsmeow/adapter.go:354`); and
- the fleet runs one daemon per Dokku app, one app per account (`wa-personal`,
  `wa-burocracy`), each with its own REST listener, token DB, allowlist and audit log.

So **account = profile = daemon process = Dokku app**. They are one-to-one, and that is
by design (per-account blast radius, per-account rate limiter and warmup).

**Adding a new account, end to end.**

1. *Provision* an empty daemon. This is an operator deploy step: create the Dokku app,
   its storage mount and its env (`WAD_REST_HTTP_ADDR`, `WAD_REST_TOKEN_DB`,
   `WAD_HEALTH_HTTP_ADDR`), and issue the first admin token with the one-shot
   `wad token issue` (`cmd/wad/token_cmd.go:96`). It is outside every slice of this spec
   (§8) and is the same step any new service needs.
2. *Pair* the new, unpaired daemon through **its own** `/admin/pair` page: QR in the
   browser, scanned by the owner's phone (S6).
3. *Configure* allowlist and tokens through the same daemon's `/admin` (S7).

Re-adding an account after an unlink or a session wipe is steps 2–3 only, on the existing
daemon.

**Decision: per-daemon `/admin` on the existing REST listener.**

| Option | Verdict | Reason |
|---|---|---|
| **A. Per-daemon `/admin`** mounted on the existing REST mux (`internal/adapters/primary/rest/server.go:185`) | **Chosen** | No new service, one binary, one auth store (110d), one audit log per account. Everything "add an account" needs *after* provisioning (pairing, allowlist, tokens) is state owned by that daemon, so the daemon is the only place it can be changed without a second credential path. |
| B. Separate aggregator service (one UI over N daemons) | Rejected | Its one exclusive capability is provisioning a daemon, which means holding Dokku-host (shell) credentials: a new high-value secret, a new service, and a blast radius of more than one account. Provisioning is a deploy, out of scope by §8. For everything else it would be a proxy that needs N admin tokens. |
| C. Static fleet index (an HTML page of links to each daemon's `/admin`) | Deferred | Harmless and cheap, but it adds no capability over a bookmark. Revisit if the account count grows. |

Proof that A is sufficient: of the three steps above, step 1 is a deploy in every design
(option B would only move where the Dokku credential lives), and steps 2–3 mutate state
that only the target daemon holds. Nothing in "add an account" requires one process to
reach two accounts.

## 3. Web UI requirements

All pages are server-rendered with `html/template` (contextual auto-escaping), with no JS
framework, no CDN and no inline script. Every response under `/admin` carries:

```
Content-Security-Policy: default-src 'self'; frame-ancestors 'none'; base-uri 'none'; form-action 'self'
Cache-Control: no-store
Referrer-Policy: no-referrer
X-Content-Type-Options: nosniff
```

This is stricter than `/docs`, which allows `cdn.jsdelivr.net` and `'unsafe-inline'`
(`internal/adapters/primary/rest/server.go:212-213`). Styles come from one
same-origin stylesheet, `/admin/static/admin.css`.

- **FR-116-01 Status page** (`GET /admin`) shows, for this daemon's account:
  - paired / connected (from the S1 health fields, never from session existence);
  - the account number **masked to its last four digits**;
  - linked-device slot (device id) and `session_since`;
  - `last_persisted_event_at` (RFC 3339) and `ingest_lag_s`;
  - `storage_writable` and `free_bytes`;
  - `health` (`healthy | degraded | down`) with its reason list.

  Given a daemon whose last persist failed, when `/admin` renders, then the page shows
  `degraded` with reason `storage_unwritable`. Property: every value on the page comes
  from the same `health` result `/readyz` uses, so the two can never disagree.
- **FR-116-02 Add account = pair** (`GET /admin/pair`, `POST /admin/pair/start`). An
  unpaired daemon offers "Pair this account". It starts the QR flow and shows the QR in
  the browser through `internal/pairhtml`, extended with a strict-CSP render mode
  (external stylesheet, QR as a same-origin `GET /admin/pair/{nonce}/qr.png` instead of a
  `data:` URI). Loading / QR / paired states reuse the existing template states. A paired
  daemon shows "already paired" and no QR (matching `doPair`'s refusal,
  `internal/app/method_pair.go`).
- **FR-116-03 Unlink / logout** (`POST /admin/session/logout`) sits behind an explicit
  confirmation page. The operator types the profile name, then the form posts with CSRF
  and calls the existing `session.logout` (`internal/app/method_logout_all.go:42`). No
  link, GET, or single click unlinks.
- **FR-116-04 Allowlist** (`GET /admin/allowlist`, `POST /admin/allowlist/{add,remove}`):
  lists entries with their per-action grants (`read`, `send`, `group.add`,
  `group.create`), and adds or removes per action through the existing `allow` op
  handlers (`cmd/wad/methods.go:43-48`). JIDs are masked in the list view; the full value
  appears only in the add/remove form the operator types into.
- **FR-116-05 Tokens** (`GET /admin/tokens`, `POST /admin/tokens/{issue,revoke}`): lists
  `id, name, scope, created, expires, last used, revoked`
  (`internal/adapters/secondary/sqlitetokens/store.go:51-60`). Issue shows the raw secret
  **once**, on the POST response only, with `Cache-Control: no-store`, matching the
  store's show-once contract (`store.go:49-50`). It is never re-displayed, logged, or put
  in a redirect URL. An admin session may revoke any token except the one backing its own
  session; that one is "log out".
- **FR-116-06 Audit tail** (`GET /admin/audit`): read-only view of the last 200 audit
  records, newest first, JIDs masked, verified against the HMAC chain
  (`internal/adapters/secondary/slogaudit/chain.go` `VerifyChain`). A broken chain shows
  as a banner, not as missing rows.
- **FR-116-07 Login** (`GET/POST /admin/login`): the operator pastes an **admin-scope**
  token into a POST form. The daemon verifies it against the 110d store and issues a
  server-side session. A read or send token is refused with the same page and message as
  a wrong token (no scope oracle).

## 4. Security requirements and threat model

A QR shown by `/admin/pair` grants a **full device link** to the account: whoever scans it
can read and send as the owner. It is treated as a credential throughout.

- **SR-116-01 Admin scope only.** Every `/admin` route except `login` and
  `static/admin.css` requires a session created from an admin-scope token. With env-var
  single-token auth (110a, where there are no scopes), `/admin` is not mounted at all.
- **SR-116-02 Never public.** `/admin` is off unless `WAD_ADMIN_ENABLE=1`. When on, it
  answers `404` (indistinguishable from an unmounted route) unless **both** hold: the
  request `Host` is in `WAD_ADMIN_HOSTS` (an explicit list; empty means deny all), and the
  request carries none of `CF-Connecting-IP`, `CF-Ray`, `CDN-Loop: cloudflare`, or
  `True-Client-IP`. Those headers mark a request that came through the public CDN
  front-end, which an attacker on that path cannot strip. The supported reach paths are
  loopback and the tailnet. The route-level gate holds even if the network binding is
  wrong.
- **SR-116-03 CSRF.** Every state-changing route is POST-only and carries a per-session
  synchronizer token in a hidden field, compared in constant time. The request must also
  carry `Origin` equal to the request's own scheme+host, or, when `Origin` is absent,
  `Sec-Fetch-Site: same-origin`.
- **SR-116-04 Session cookie.** `__Host-wa_admin=<256-bit random id>; Path=/; HttpOnly;
  Secure; SameSite=Strict`. Server-side session store, in memory. Idle timeout 15 min,
  absolute 8 h, id rotated at login, all sessions dropped on daemon restart and on
  revocation of the backing token.
- **SR-116-05 No secret in URLs or logs.** Tokens, session ids, CSRF tokens and QR codes
  never appear in a URL, a redirect, a log line, an audit record, or an error body. The
  QR image URL carries a single-use nonce, never the code.
- **SR-116-06 QR handling.** Bound to the session that started pairing; one viewer (a
  second session gets "pairing in progress"); `Cache-Control: no-store`; each QR image
  nonce valid for one fetch and at most 60 s; the whole pairing window closes after
  3 min. A pairing started from `/admin` does not print the QR to stderr or write the
  temp-file page (`internal/adapters/secondary/whatsmeow/pair.go:135`, `:139`). The CLI
  path keeps its current behaviour until a separate slice decides it.
- **SR-116-07 Login rate limit.** Max 5 failed logins per client address per 15 min and
  30 per hour per daemon, then `429` with `Retry-After`. Token verification stays the
  store's constant-time hash comparison.
- **SR-116-08 Audit every admin action.** Login success/failure, pair start/success/
  timeout, logout, allowlist add/remove, token issue/revoke and admin settings changes
  each append one record with `via:"admin"`, through the existing append-only HMAC-chained
  audit (`internal/adapters/secondary/slogaudit/chain.go`). The one-shot
  `wad token issue|revoke` CLI writes no audit record today (`cmd/wad/token_cmd.go` has no
  audit call), so the UI path must not inherit that gap.
- **SR-116-09 No agent path to admin mutations.** No MCP tool, at any scope, mutates the
  allowlist, pairing, session or tokens. Those stay human-only, in `/admin` or the CLI.
  A prompt-injected agent must not be able to grant itself a recipient (CLAUDE.md
  §Safety 5).

| # | Asset | Threat | Control | Test (named in its slice) |
|---|---|---|---|---|
| T1 | Admin surface | Reached through the public CDN-fronted host | SR-116-02 gate: `WAD_ADMIN_ENABLE`, `WAD_ADMIN_HOSTS`, CDN-header deny, `404` | `TestAdmin_DeniedOnPublicHost` (S5) |
| T2 | Admin token | Leaks via URL, Referer or logs | POST-only login, `Referrer-Policy: no-referrer`, log redaction | `TestAdmin_TokenNeverLogged` (S5) |
| T3 | Session cookie | Theft, fixation, cross-site send | SR-116-04 attributes, rotation, idle/absolute expiry | `TestAdmin_SessionCookieAttributes` (S5) |
| T4 | Allowlist / tokens / session | CSRF from another origin | SR-116-03 synchronizer token + Origin / Sec-Fetch-Site | `TestAdmin_CSRFRejected` (S7) |
| T5 | Pairing QR (device link) | Logged, cached, persisted, shoulder-surfed, replayed | SR-116-06: no stderr/temp file, no-store, single viewer, 60 s nonce, 3 min window | `TestAdminPair_QRNotPersisted`, `TestAdminPair_SingleViewer` (S6) |
| T6 | Operator browser | XSS from contact names, push names or audit detail | `html/template` escaping, CSP `default-src 'self'`, no inline script | `TestAdmin_CSPHeader`, `TestAdmin_EscapesUntrustedNames` (S5) |
| T7 | Operator browser | Clickjacking the confirm buttons | `frame-ancestors 'none'` | `TestAdmin_CSPHeader` (S5) |
| T8 | Admin login | Token brute force | SR-116-07 rate limits, constant-time verify | `TestAdmin_LoginRateLimited` (S5) |
| T9 | Allowlist | Prompt-injected agent grants itself a recipient | SR-116-09: no MCP mutation tools at any scope | `TestMCPScope_NoAdminMutationTools` (S2) |
| T10 | Tokens | Escalation by a non-admin; untraceable issuance | Admin session only; secret shown once; issue/revoke audited | `TestAdminTokens_IssueShownOnceAndAudited` (S7) |
| T11 | Account link | Accidental or forged unlink | Typed-name confirm + POST + CSRF + audit | `TestAdminLogout_RequiresTypedConfirm` (S6) |
| T12 | Health endpoint | Unauthenticated `/readyz` leaks data | `/readyz` body is a reason enum only; byte counts and timestamps only in authenticated `health`/`status` | `TestReadyz_BodyIsReasonOnly` (S1) |
| T13 | Media | Agent bulk-exfiltrates or picks attachments by time proximity | Chat + sender/caption filter required before any fetch; size cap; channel-wrapped captions; `via:"mcp"` audit | `TestMCPMedia_RequiresNarrowingFilter` (S3) |
| T14 | Daemon | Job-queue exhaustion via async sync | Bounded job table, per-token concurrency cap | `TestSyncJobs_Bounded` (S4) |

## 5. MCP requirements ("really good MCP")

- **FR-116-10 Typed tool errors.** Every refusal from either transport becomes a
  tool-execution error (SEP-1303) whose structured content is

  ```json
  {"code": -32117, "reason": "message_not_found",
   "remediation": "No stored message has this id. List candidates with wa_get_thread or wa_list_media.",
   "required_scope": "admin"}
  ```

  `required_scope` appears only on `scope_insufficient`. `reason` comes from a closed
  enum generated from the `app.RPCWire` table and the `internal/app/errors.go` catalog,
  with one row per code or sentinel, never a numeric band: `message_not_found`,
  `media_not_cached`, `media_unsupported`, `media_too_large`, `not_allowlisted`,
  `scope_insufficient`, `not_paired`, `session_wiped`, `disconnected`, `rate_limited`,
  `warmup_active`, `invalid_params`, `storage_unwritable`, `upstream_error`, `internal`.
  For `not_allowlisted` the body stays constant, preserving FR-050 (no recipient echo),
  and the remediation names the human action ("ask the operator to grant it in
  /admin/allowlist or with `wa allow add`; do not retry until they confirm"). Both
  transports get the code from `app.RPCWire`. `dispatcherCaller`'s private mapping
  (`cmd/wad/mcp_http.go:43-50`) and the numeric bands (`internal/mcpbridge/errors.go:12-27`)
  are replaced. Closes F1–F3 on MCP.
- **FR-116-11 Scope-filtered tool list.** The tool set is a pure function of the caller's
  scope: `read` gets read tools; `send` gets read plus send tools under the configured
  send mode; `admin` gets send plus admin-only **read** tools (`wa_audit_tail`). Each
  tool's description and `_meta` state its required scope. Stdio maps `--read-only` to
  `read` and the default to `send`. Property: for every scope s and every listed tool t,
  `required_scope(t) ≤ s`, and calling an unlisted tool name is "unknown tool", never a
  scope error.
- **FR-116-12 Scope introspection.** A read-scope method `auth.whoami` (MCP: part of
  `wa_status`) returns the held scope and, for a given method name, its required scope.
  It is derived from `rest.RequiredScope` (`internal/adapters/primary/rest/scope.go:206`)
  so it cannot drift. Closes F3 before the fact.
- **FR-116-13 Truthful `wa_status`.** Returns `health: healthy | degraded | down` with
  `reasons ⊆ {not_paired, disconnected, ingest_stale, storage_unwritable,
  soft_stale}` plus the S1 fields, and the account number masked to its last four digits,
  never the full JID. `down` means not paired, or storage unwritable; `degraded` means any
  other reason.
- **FR-116-14 `wa_allowlist_check`.** Read scope. Input: recipient (name or number,
  resolved like `wa_resolve_contact`) and action (`send`, `read`, `group.add`,
  `group.create`). Output: `{allowed: bool, action, remediation?}`. It reads the in-memory
  allowlist without mutating it, is audited as a decision record, and is rate-limited with
  the read surface. It does not reveal which other actions or recipients are allowlisted.
  Backed by a new `allow.check` method classified `ScopeRead`; `allow` op=list stays admin.
- **FR-116-15 Media read tools.**
  - `wa_list_media {chat, sender?, caption?, media_type?, since?, until?, limit≤50}`
    wraps `media.list`. `chat` is required, **and** at least one of `sender`/`caption` is
    required. `since`/`until` may only narrow a request that already has those filters,
    so the tool cannot select attachments by timestamp proximity. The result echoes
    `appliedFilters` (`cmd/wad/media_list_method.go:58`). Inbound captions stay inside the
    channel envelope.
  - `wa_get_media {chat, message_id, max_bytes?}` fetches one object named by a prior
    list result (`chat` + `message_id` must match a row). Default cap 5 MiB, hard cap
    16 MiB (below `domain.MaxInboundMediaBytes`). Over the cap it returns
    `media_too_large` with the object's size and the operator CLI command, and no bytes.
    Bytes go out as an MCP embedded resource, through `media.fetchBytes` chunks on stdio
    (socket frame cap, `internal/app/method_media.go:141-152`).
  - Both are read scope, toolset `messages`, audited with `via:"mcp"`.
- **FR-116-16 No synchronous call over 10 s.** No MCP tool and no REST method blocks
  longer than 10 s. Chat-scoped history sync becomes a job: `sync.force` with
  `async:true` returns `{job_id, state:"queued"}` at once, and `jobs.get {job_id}` returns
  `queued | running | done | failed | expired` with the result. MCP adds
  `wa_sync_history` (send scope) and `wa_job_status` (read scope). Jobs live in memory, at
  most 16 per daemon and 2 per token, expire 1 h after completion, and never outlive the
  process. `wa_wait_for_reply` clamps each call to 10 s and returns `{timed_out:true}` for
  the agent to re-poll, instead of 60–300 s (`internal/mcpbridge/tools.go:205-211`).
- **FR-116-17 Unambiguous time and caption fields.** Every MCP result carries time as a
  pair: `<name>_at` (RFC 3339, UTC) and `<name>_unix_s`. A bare `timestamp` never appears
  in a new field. Each message gets explicit `has_caption: bool` and `caption`, where the
  inbound `caption` is its own FR-005a envelope (the existing `channel` field is kept for
  compatibility), so a consumer never finds a caption by parsing `channel`.
- **FR-116-18 One eval case per F-class** in `internal/mcpbridge/evals/evals_test.go`
  (the `TestEvalSet` table): `f1-missing-message-is-not-found`,
  `f2-preflight-before-send`, `f3-scope-shown-before-call`, `f4-status-says-not-paired`,
  `f5-status-says-storage-degraded`, `f6-sync-returns-job-id`,
  `f7-time-fields-carry-units`, plus `media-list-requires-sender-or-caption`.
  `tools.snapshot.json` is updated in the same slice as each tool change.

## 6. Health requirements

- **FR-116-20** `health` and `status` add `storage_writable` (bool), `free_bytes` (data
  dir filesystem), `last_persisted_event_at` / `last_persisted_event_unix_s` (newest
  successful `messages.db` insert), `last_received_event_at` / `last_received_event_unix_s`
  (today's `lastEventTs`, renamed alongside the old field), and `ingest_lag_s`. Fields are
  additive under `omitempty` (no schema v2 bump).
- **FR-116-21** `ingest_lag_s = max(0, last_received − last_persisted)`. It measures
  "arrived but not stored", not quietness: an idle account has lag 0 and stays healthy, so
  low-traffic accounts do not flap. `ingest_stale` holds when `ingest_lag_s >
  WAD_INGEST_STALE_SEC` (default 300) **while connected**.
- **FR-116-22** `storage_writable` is false when any of these holds: the last persist
  attempt failed within the last 5 min; `free_bytes < WAD_MIN_FREE_BYTES` (default
  64 MiB); or a background probe (every 30 s, off the request path) fails to write+fsync
  a 1-byte file in the data dir. `/readyz` reads cached values only and stays
  sub-millisecond (`cmd/wad/health_http.go:30-32`).
- **FR-116-23** `/readyz` returns `200 ready` only when paired AND connected AND
  storage writable AND not ingest-stale. Otherwise it returns `503` with one reason token
  per line from `{not_paired, disconnected, storage_unwritable, ingest_stale}`. `IsReady`
  stops being session-existence (`internal/app/method_health.go:135`), and `status.connected`
  stops being session-existence (`internal/app/method_status.go:49`). Liveness `/healthz`
  is unchanged (spec 109 FR-004).
- Given a connected daemon whose last insert failed with `ENOSPC`, when `/readyz` is
  probed, then it answers `503` with `storage_unwritable`. Property: `/readyz` is 200
  iff `health.health == "healthy"`.

## 7. Slices

Each slice is one PR of about 400 changed lines or fewer, touches no `specs/` file
(spec-guard), and runs its acceptance with `GOMAXPROCS=2 GOFLAGS=-p=2` (the desktop cgroup
refuses default parallelism). The test names below are the contract: the slice adds
them, and they must pass.

Order follows the brief, with one written reason: S1 goes first because S2's `wa_status`
and S5's page both read the truthful health fields, and a page built on the current
`IsReady` would display the F5 lie.

#### S1 — health truthfulness (F5)

FR-116-20..23 and the `status.connected` fix, plus `wa_status` health and masking
(FR-116-13). Incident-class (R32): the PR body carries the rollback (revert one commit;
`/readyz` returns to session-only) and the canary order `wa-burocracy` before
`wa-personal`, verifying `/readyz` 200 and an advancing `last_persisted_event_at` between
the two.

Acceptance:

```bash
cd ~/Documents/Code/yolo-labz/wa
export GOMAXPROCS=2 GOFLAGS=-p=2
go build ./... && go vet ./...
go test -race -shuffle=on -count=1 -run 'TestReadyz_|TestHealth_Storage|TestHealth_IngestLag|TestStatus_ConnectedIsLive' ./cmd/wad/ ./internal/app/
go test -race -shuffle=on -count=1 -run 'TestReadyz_BodyIsReasonOnly|TestReadyz_StorageUnwritable|TestReadyz_IdleAccountStaysReady' ./cmd/wad/
go test -count=1 -run 'TestEvalSet/f5-status-says-storage-degraded|TestEvalSet/f4-status-says-not-paired' ./internal/mcpbridge/evals/
```

#### S2 — typed MCP/RPC errors + scope-filtered tool list (F1–F3)

FR-116-10/11/12, `allow.check` + `wa_allowlist_check` (FR-116-14), and SR-116-09. Both
MCP transports take codes from `app.RPCWire`; the REST `403` adds
`{reason:"scope_insufficient", required_scope, granted_scope}` to the problem body and
keeps the wire code. If the diff exceeds the line budget, split it as S2a (errors) and S2b
(scope list + `allow.check`), in that order.

Acceptance:

```bash
cd ~/Documents/Code/yolo-labz/wa
export GOMAXPROCS=2 GOFLAGS=-p=2
go build ./... && go vet ./...
go test -race -shuffle=on -count=1 -run 'TestRemediate_EveryCatalogCode|TestDispatcherCaller_UsesRPCWire' ./internal/mcpbridge/ ./cmd/wad/
go test -race -shuffle=on -count=1 -run 'TestMCPScope_|TestScopeRefusal_NamesRequiredScope|TestAllowCheck_' ./cmd/wad/ ./internal/adapters/primary/rest/
go test -count=1 -run 'TestEvalSet/f1-|TestEvalSet/f2-|TestEvalSet/f3-|TestToolSnapshot' ./internal/mcpbridge/evals/
```

#### S3 — media read tools

FR-116-15: `wa_list_media` and `wa_get_media`, the narrowing-filter rule, size caps,
channel-wrapped captions, and audit `via:"mcp"`.

Acceptance:

```bash
cd ~/Documents/Code/yolo-labz/wa
export GOMAXPROCS=2 GOFLAGS=-p=2
go build ./... && go vet ./...
go test -race -shuffle=on -count=1 -run 'TestMCPMedia_RequiresNarrowingFilter|TestMCPMedia_SizeCap|TestMCPMedia_CaptionStaysWrapped|TestMCPMedia_Audited' ./internal/mcpbridge/
go test -count=1 -run 'TestEvalSet/media-list-requires-sender-or-caption|TestToolSnapshot' ./internal/mcpbridge/evals/
```

#### S4 — async history-sync jobs (F6)

FR-116-16: `sync.force` with `async:true`, `jobs.get`, `wa_sync_history`,
`wa_job_status`, and the 10 s clamp on `wa_wait_for_reply`. Time-sensitive tests use
`testing/synctest` with in-memory fakes, and the job reaper gets goleak coverage (R31).

Acceptance:

```bash
cd ~/Documents/Code/yolo-labz/wa
export GOMAXPROCS=2 GOFLAGS=-p=2
go build ./... && go vet ./...
go test -race -shuffle=on -count=1 -run 'TestSyncJobs_|TestNoMethodBlocksOver10s|TestWaitForReply_Clamped' ./cmd/wad/ ./internal/mcpbridge/
go test -count=1 -run 'TestEvalSet/f6-sync-returns-job-id|TestToolSnapshot' ./internal/mcpbridge/evals/
```

#### S5 — admin UI, read-only status page

FR-116-01, FR-116-07, SR-116-01/02/04/05/07; mounts `/admin` behind `WAD_ADMIN_ENABLE`
and `WAD_ADMIN_HOSTS`. There are no state-changing routes except login and logout of the
admin session itself.

Acceptance:

```bash
cd ~/Documents/Code/yolo-labz/wa
export GOMAXPROCS=2 GOFLAGS=-p=2
go build ./... && go vet ./...
go test -race -shuffle=on -count=1 -run 'TestAdmin_DeniedOnPublicHost|TestAdmin_DisabledByDefault|TestAdmin_RequiresAdminScope|TestAdmin_SessionCookieAttributes|TestAdmin_CSPHeader|TestAdmin_EscapesUntrustedNames|TestAdmin_TokenNeverLogged|TestAdmin_LoginRateLimited' ./internal/adapters/primary/rest/ ./cmd/wad/
! grep -rnE 'cdn\.|https?://[a-z]' internal/adapters/primary/rest/admin*.html 2>/dev/null
```

#### S6 — admin UI pairing (F4)

FR-116-02/03, SR-116-06: the strict-CSP render mode for `internal/pairhtml`, the QR image
endpoint, and the typed-confirm logout. The tests use the whatsmeow fake client. No live
pairing in this slice.

Acceptance:

```bash
cd ~/Documents/Code/yolo-labz/wa
export GOMAXPROCS=2 GOFLAGS=-p=2
go build ./... && go vet ./...
go test -race -shuffle=on -count=1 -run 'TestAdminPair_QRNotPersisted|TestAdminPair_SingleViewer|TestAdminPair_NonceSingleUse|TestAdminPair_WindowExpires|TestAdminPair_AlreadyPaired|TestAdminLogout_RequiresTypedConfirm' ./internal/adapters/primary/rest/ ./cmd/wad/ ./internal/adapters/secondary/whatsmeow/
go test -count=1 ./internal/pairhtml/
```

#### S7 — allowlist + token management

FR-116-04/05/06, SR-116-03/08: allowlist add/remove per action, token issue (shown once)
and revoke, audit tail, and CSRF on every POST.

Acceptance:

```bash
cd ~/Documents/Code/yolo-labz/wa
export GOMAXPROCS=2 GOFLAGS=-p=2
go build ./... && go vet ./...
go test -race -shuffle=on -count=1 -run 'TestAdmin_CSRFRejected|TestAdminAllowlist_|TestAdminTokens_IssueShownOnceAndAudited|TestAdminTokens_CannotRevokeOwnSessionToken|TestAdminAudit_TailVerifiesChain' ./internal/adapters/primary/rest/ ./cmd/wad/
```

#### S8 — export and time fields (F7)

FR-116-17 on the RPC surface as well (`export`, `history`, `messages.list`,
`media.list`): add `timestamp_unix_s` + `timestamp_at`, `has_caption`, and the wrapped
inbound `caption`. Existing `timestamp` and `channel` stay for compatibility, with a
deprecation note in `docs/manual.md`.

Acceptance:

```bash
cd ~/Documents/Code/yolo-labz/wa
export GOMAXPROCS=2 GOFLAGS=-p=2
go build ./... && go vet ./...
go test -race -shuffle=on -count=1 -run 'TestWireMessage_TimeFieldsCarryUnits|TestWireMessage_InboundCaptionWrapped|TestExport_' ./cmd/wad/ ./cmd/wa/
go test -count=1 -run 'TestEvalSet/f7-time-fields-carry-units' ./internal/mcpbridge/evals/
```

**Open question for `/speckit:clarify`, not decided here:** should `message.edit` of the
caller's own message inside the edit window drop from `ScopeAdmin` to `ScopeSend`, the way
`message.revoke` with `scope:"self"` already does (`scope.go:185-214`)? F3's root
annoyance is that a send token cannot correct its own send. Changing a scope bar is a
policy decision, so this spec makes the requirement visible (FR-116-12) and leaves the
bar unchanged.

## 8. Out of scope for every slice

- No deploy, no Dokku app change, no env change on a running daemon, no new service.
- No live pairing, no unlink, no logout against a real account; pairing is tested with the
  fake whatsmeow client only.
- No token issued or revoked on a live daemon, and no allowlist change on a live daemon.
- No public exposure: no CDN route, no DNS change, no tunnel; `/admin` stays off by
  default (`WAD_ADMIN_ENABLE` unset).
- No MCP tool that mutates allowlist, pairing, session or tokens (SR-116-09).
- No change to the FR-005a envelope contract; new caption fields wrap, never unwrap.
- No renumbering of existing wire codes; `reason` disambiguates where codes collide
  (`-32099`).
