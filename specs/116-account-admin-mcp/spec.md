# Feature 116 — account admin web UI + agent-safe MCP

**Branch**: `116-account-admin-mcp` (design-only — no code in this PR)
**Status**: drafted 06/11/2026; revision 1 on 07/11/2026; revisions 2 and 3 on 08/11/2026 after re-review / Gate 3 (see Review log); `plan.md` / `tasks.md` follow in their own PRs
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

F1–F7 each cost an agent a slice or produced a false diagnosis; F9/F10 add the recovery
failure classes from the sanitised addendum. Each was checked on `cc313dc` (R29: cite
the code, not the anecdote). A class fixed outright is dropped from fix scope; partial
fixes are recorded below. F8 remains an adjacent media-tool gap.

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

### F9 — no backfill after a persist-failure window or restart

A storage outage can lose server-acked messages even while the socket is live. Restarting
the daemon does not recover that missed window. This is not the soft-stale reconnect
failure class: `BackfillRecent` delegates to `ForceHistorySync`
(`internal/adapters/secondary/whatsmeow/backfill.go:55-59`), and the composition root
wires it into the watchdog (`cmd/wad/main.go:772`). The watchdog calls it only after a
successful recover reconnect (`cmd/wad/watchdog.go:359-381`), not on storage recovery
or daemon start. S1b adds deferred per-chat recovery after a real live message provides
a valid anchor; it does not claim that clearing readiness restores lost messages.

Verified on main: cmd/wad/watchdog.go:381 — still present

### F10 — sync force cannot reach a tail gap

The on-demand request returns messages **before** a real anchor
(`internal/adapters/secondary/whatsmeow/sync.go:88-92`). Chat-scoped force sync chooses
the newest stored message (`sync.go:164-173`). When that message predates the outage,
its preceding history cannot include the newer tail gap, yet the current result can
report `Requested=true` (`sync.go:174`). A successful request is not proof of recovery.
S1b requires `gap_reachable:false` with a reason in this case and waits for the first
post-recovery persisted live message. No synthetic or future anchor is specified.

Verified on main: internal/adapters/secondary/whatsmeow/sync.go:164 — still present

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

**Decision: per-daemon `/admin` inside the existing `wad` process, on its own loopback
listener.**

| Option | Verdict | Reason |
|---|---|---|
| **A. Per-daemon `/admin`** served by a second `http.Server` in the same `wad` process, bound to `WAD_ADMIN_ADDR` (loopback only, SR-116-02) | **Chosen** | No new service, one binary, one auth store (110d), one audit log per account. Everything "add an account" needs *after* provisioning (pairing, allowlist, tokens) is state owned by that daemon, so the daemon is the only place it can be changed without a second credential path. A separate listener, rather than a route on the REST mux (`internal/adapters/primary/rest/server.go:185`), keeps the admin surface off whatever network path reaches REST (review B1). |
| B. Separate aggregator service (one UI over N daemons) | Rejected | Its one exclusive capability is provisioning a daemon, which means holding Dokku-host (shell) credentials: a new high-value secret, a new service, and a blast radius of more than one account. Provisioning is a deploy, out of scope by §8. For everything else it would be a proxy that needs N admin tokens. |
| C. Static fleet index (an HTML page of links to each daemon's `/admin`) | Deferred | Harmless and cheap, but it adds no capability over a bookmark. Revisit if the account count grows. |

Proof that A is sufficient: of the three steps above, step 1 is a deploy in every design
(option B would only move where the Dokku credential lives), and steps 2–3 mutate state
that only the target daemon holds. Nothing in "add an account" requires one process to
reach two accounts.

## 3. Web UI requirements

All pages are server-rendered with `html/template` (contextual auto-escaping), with no JS
framework, no CDN and no inline script or style. Templates and the one stylesheet live
in a new primary adapter, `internal/adapters/primary/admin/` (`templates/*.html`,
`static/admin.css`). Every response from the admin listener carries:

```
Content-Security-Policy: default-src 'self'; frame-ancestors 'none'; base-uri 'none'; form-action 'self'
Cache-Control: no-store
Referrer-Policy: no-referrer
X-Content-Type-Options: nosniff
```

This is stricter than `/docs`, which allows `cdn.jsdelivr.net` and `'unsafe-inline'`
(`internal/adapters/primary/rest/server.go:212-213`).

- **FR-116-01 Status page** (`GET /admin`) shows, for this daemon's account:
  - `health` (`healthy | degraded | down`) and its reason list, exactly as §6's truth
    table computes them;
  - paired / connected (live, from S1, never from session existence);
  - the account number **masked to its last four digits**;
  - linked-device slot (device id) and `session_since`;
  - `last_received_at`, `last_persisted_at`, `ingest_lag_s`;
  - `storage` (`ok | unknown | failed`), `last_persist_failure_at`,
    `persist_failures_total`, `persist_logic_errors_total`, `free_bytes`.

  Given a daemon whose last database write failed, when `/admin` renders, then the page
  shows `down` with reason `storage_unwritable`. Property: the page and `/readyz` read the
  same cached health snapshot, so they cannot disagree.
- **FR-116-02 Add account = pair** (`GET /admin/pair`, `POST /admin/pair/start`,
  `POST /admin/pair/cancel`). An unpaired daemon offers "Pair this account". The flow,
  its cancellation and its credential handling are SR-116-06. The QR is shown through
  `internal/pairhtml`, extended with a strict-CSP render mode: external stylesheet, and the
  QR served as a same-origin `GET /admin/pair/{nonce}/qr.png` instead of a `data:` URI
  (`internal/pairhtml/pairhtml.go:50`, `:101` are what the current template uses). A
  paired daemon shows "already paired" and no QR, matching `doPair`'s refusal
  (`internal/app/method_pair.go`).
- **FR-116-03 Unlink / logout** (`POST /admin/session/logout`) sits behind a confirmation
  page where the operator types the profile name. The form posts with CSRF and calls the
  existing `session.logout` (`internal/app/method_logout_all.go:42`). No link, GET, or
  single click unlinks.
- **FR-116-04 Allowlist** (`GET /admin/allowlist`, `POST /admin/allowlist/{add,remove}`):
  lists entries with their per-action grants (`read`, `send`, `group.add`,
  `group.create`), and adds or removes per action through the existing `allow` op
  handlers (`cmd/wad/methods.go:43-48`). JIDs are masked in the list view; the full value
  appears only in the add/remove form the operator types into.
- **FR-116-05 Tokens** (`GET /admin/tokens`, `POST /admin/tokens/{issue,revoke}`): lists
  `id, name, scope, created, expires, last used, revoked`
  (`internal/adapters/secondary/sqlitetokens/store.go:51-60`). Issue shows the raw secret
  **once**, on the POST response only, matching the store's show-once contract
  (`store.go:49-50`). It is never re-displayed, logged, or put in a redirect URL. Revoking
  a token drops every admin session it backs and cancels its jobs and pairing flows,
  including revocation by the separate `wad token revoke` process. Request-time store
  revalidation and the ≤30 s sweep enforce this (SR-116-04), not a UI callback. An
  admin session cannot revoke the token behind its own session; that is "log out".
- **FR-116-06 Audit tail** (`GET /admin/audit`): read-only view of the last 200 audit
  records, newest first, JIDs masked, verified against the HMAC chain
  (`internal/adapters/secondary/slogaudit/chain.go` `VerifyChain`). A broken chain shows
  as a banner, not as missing rows.
- **FR-116-07 Login** (`GET/POST /admin/login`): the operator pastes an **admin-scope**
  token into a POST form protected by pre-login CSRF (SR-116-03). The daemon verifies the
  token against the 110d store and issues a server-side session. A read or send token is
  refused with the same page and message as a wrong token, so the form is not a scope
  oracle.

## 4. Security requirements and threat model

A QR shown by `/admin/pair` grants a **full device link** to the account: whoever scans it
can read and send as the owner. It is treated as a credential throughout. Network
location is established only from the listener's bind address and the TCP peer of each
accepted connection. No request header is ever used to decide where a request came from
or which scheme it used.

- **SR-116-01 Admin scope only.** Every admin route except `GET /admin/login`,
  `POST /admin/login` and `GET /admin/static/admin.css` requires a session created from an
  admin-scope token. The admin listener refuses to start unless the scoped token DB
  (`WAD_REST_TOKEN_DB`, 110d) is configured: env-var single-token auth (110a) has no
  scopes, so it gets no admin UI.
- **SR-116-02 Separate loopback listener, never public.**
  - `/admin` is served only by a second `http.Server` in the same `wad` process, bound to
    `WAD_ADMIN_ADDR`. Unset (the default) means no admin listener at all. The REST mux
    never registers any `/admin` route, so `/admin` on the REST listener is the mux's
    ordinary `404`.
  - `WAD_ADMIN_ADDR` must be an IP literal with an explicit port, in `127.0.0.0/8` or
    `::1`. At startup the daemon refuses, logs, and runs without the admin listener when
    the address is a hostname (DNS could move it), a wildcard (`0.0.0.0`, `::`), any
    non-loopback address (RFC 1918, the tailnet ranges `100.64.0.0/10` and
    `fd7a:115c:a1e0::/48`, public), or the same port as `WAD_REST_HTTP_ADDR` or
    `WAD_HEALTH_HTTP_ADDR`. REST and health keep running. The Dockerfile does not
    `EXPOSE` the port (today it exposes only `8080`, `Dockerfile:141`).
  - Defense in depth: every accepted connection whose TCP peer (`r.RemoteAddr` of the
    accepted socket) is not loopback gets `404`, whatever its `Host`.
  - *Why loopback only, not loopback-or-tailnet (alternative to the review hint).* A
    tailnet bind would serve plain HTTP to a non-loopback origin, which SR-116-04
    rejects, so it would add a bind that can never serve. Tailnet reach comes instead from
    `tailscale serve` terminating HTTPS in front of the loopback listener (§7, deferred
    operator runbook).
  - Reach paths, none configured by any slice: (1) host-native daemon: `tailscale serve`
    (HTTPS, ts.net certificate) proxying to `WAD_ADMIN_ADDR`, never `tailscale funnel`;
    (2) containerized daemon: the runtime image is distroless with no shell or socat
    (`Dockerfile:102`), so S5 adds a one-shot `wad admin-tunnel` that copies stdin/stdout
    to `WAD_ADMIN_ADDR` inside the container. The operator runs a local loopback forwarder
    whose per-connection command is `ssh <host> docker exec -i <container> wad
    admin-tunnel`, and opens `http://localhost:<port>`. The forwarder binds **127.0.0.1**
    only, never a wildcard. The daemon sees loopback; T17 records the remaining cookie
    and tunnel trust risk.
- **SR-116-03 CSRF, pre-login included.**
  - *Pre-login:* `GET /admin/login` sets `__Host-wa_admin_pre` (256-bit random, 10 min,
    `HttpOnly; Secure; SameSite=Strict; Path=/`) and embeds
    `HMAC(per-process key, pre-id)` in a hidden field. `POST /admin/login` requires the
    cookie and the field to match, compared in constant time.
  - *After login:* a per-session synchronizer token in a hidden field on every
    state-changing form, compared in constant time, rotated with the session id.
  - *Every POST*, login and logout included: `Origin` must equal the request's effective
    origin (SR-116-04). If `Origin` is absent, `Sec-Fetch-Site: same-origin` is required;
    if both are absent, the request is rejected. Every state-changing route is POST-only.
  - Every slice that adds a state-changing route ships its CSRF test and its audit test in
    the same PR (§7).
- **SR-116-04 HTTPS boundary, scheme and cookie.**
  - The admin listener speaks plain HTTP on loopback and never terminates TLS itself. A
    request is served only when its effective origin is one of:
    - (a) *loopback origin*: `Host` is `localhost:<port>`, `127.0.0.1:<port>` or
      `[::1]:<port>`, giving effective origin `http://<Host>`. Browsers treat these as
      potentially trustworthy (secure context) and accept `Secure` / `__Host-` cookies on
      them;
    - (b) *HTTPS front*: `Host` exactly matches an entry in `WAD_ADMIN_HTTPS_ORIGINS`
      (the operator's ts.net name; empty by default), giving effective origin
      `https://<Host>`. The scheme comes from this config membership alone.
      `X-Forwarded-Proto`, `Forwarded` and similar headers are ignored.
  - Any other `Host` (a plain-HTTP, non-loopback origin) gets `403` with a page naming
    the two supported reach paths, and no cookie is set.
  - *Trusted-proxy boundary:* the TCP-peer loopback check is a boundary only for
    origin (a). Behind an HTTPS front such as `tailscale serve`, every remote tailnet user
    arrives as a loopback peer, so under origin (b) the peer check proves nothing about
    the client and the admin login is the only gate. Forwarding headers stay ignored;
    the front's own tailnet ACLs are the operator's concern, not a daemon guarantee.
  - *Session cookie:* `__Host-wa_admin=<256-bit random id>; Path=/; HttpOnly; Secure;
    SameSite=Strict`. Server-side session store, in memory. Idle timeout 15 min, absolute
    8 h. The id rotates at login. All sessions drop on daemon restart and when their
    backing token is revoked or expires.
  - *Cross-process revalidation.* Every authenticated admin request (including each
    QR page render, nonce mint and image fetch) and every `jobs.get` / `jobs.cancel`
    reads its backing token’s current scope, revocation and expiry from the SQLite store
    before serving data or dispatching. Session/job records retain a token id, not the
    raw secret; add a store validity-by-id read as needed. A login-time result or positive
    in-memory cache is insufficient: `wad token revoke` writes SQLite from a separate
    process (`cmd/wad/token_cmd.go:96`,
    `internal/adapters/secondary/sqlitetokens/store.go:311`). Revoked, expired or missing
    tokens deny access immediately on the next request; store read errors fail closed.
  - A startup sweep and periodic sweep **at most every 30 s**, independent of requests,
    revalidate backing tokens and invalidate sessions/QR nonces, cancel pairing flows
    and jobs, and drop result access for revoked/expired tokens. Resistant workers retain
    their occupied pool slot (FR-116-16). Tests revoke via a second store handle, not a
    UI callback, and cover expiry with fake-clock sweep ticks.
- **SR-116-05 No secret in URLs or logs.** Tokens, session ids, CSRF tokens, pre-login
  ids and QR codes never appear in a URL, a redirect, a log line, an audit record, or an
  error body. The QR image URL carries a single-use nonce, never the code.
- **SR-116-06 Pairing lifecycle.**
  - The flow is a state machine `idle → starting → showing_qr → paired | cancelled |
    expired | failed`, bound to the admin session that started it and tagged with a random
    flow generation id. There is one flow per daemon: a second session sees "pairing in
    progress", never the QR.
  - *Cancellation triggers*, any one of: explicit `POST /admin/pair/cancel`; logout of
    the owning admin session; that session's idle or absolute expiry; revocation or
    expiry of its backing token; the 3 min window elapsing; daemon shutdown.
  - *On cancellation, in order:* cancel the pairing context (the QR channel is opened
    with it, `internal/adapters/secondary/whatsmeow/pair.go:124`); invalidate every
    outstanding QR nonce; **drop references** to the QR code string, overwrite owned
    mutable PNG buffers, then drop those references; append `pair_cancel` with the cause.
    Go strings are immutable: do not promise physical erasure of string copies. Token
    revocation/expiry is checked at every render/fetch and by the ≤30 s sweep.
  - *Late events:* a QR `code` event whose generation id is not the current flow's is
    dropped and never rendered. A `success` event after cancellation cannot be undone by
    the daemon, because WhatsApp has already linked the device. It is applied, recorded as
    `pair_success_after_cancel`, and shown as a banner on `/admin` directing the operator
    to unlink if it was not expected.
  - *QR transport:* `Cache-Control: no-store`; the server-rendered pairing page uses
    `<meta http-equiv="refresh" content="5">` (5 s cadence, no script). Each refresh
    revalidates token/flow before minting a fresh single-use nonce. A nonce lasts at most
    60 s; a fetch after cancellation, backing-token expiry or revocation is `404`. Tests
    explicitly cover daemon shutdown and token expiry, including a late code and fetch.
  - A pairing started from `/admin` neither prints the QR to stderr nor writes the temp
    pair page (`internal/adapters/secondary/whatsmeow/pair.go:135`, `:139`). The CLI path
    keeps its current behaviour until a separate slice decides it.
- **SR-116-07 Login rate limit.** The client address is the TCP peer, and forwarding
  headers are never consulted. Every admin peer is loopback (SR-116-02), so the effective
  limit is per daemon: 10 failed logins per 15 min, then `429` with `Retry-After` until
  the window passes. Existing sessions continue, and the one-shot `wad token` CLI stays the
  break-glass path. Token verification remains the store's constant-time hash compare.
- **SR-116-08 Audit every admin action.**
  - These append records with `via:"admin"` through the existing append-only
    HMAC-chained audit (`internal/adapters/secondary/slogaudit/chain.go`): login success
    and failure, logout, pair start / success / success-after-cancel / cancel (with
    cause), session logout (unlink), allowlist add/remove, and token issue/revoke.
  - The shared audit helper lands in S5a. Before any requested admin mutation (login
    included), durably append an audit intent. If that append fails, return 503 and
    **do not dispatch the mutation**. Append its outcome afterwards. If an external
    effect already occurred and the outcome append fails, show `audit_incomplete` and
    block further requested mutations until audit recovers; never claim the effect was
    undone. Automatic security invalidation/cancellation is never delayed by audit failure.
    Each mutation ships an audit-failure/no-side-effect test with its security controls.
  - The one-shot `wad token issue|revoke` CLI writes no audit record today
    (`cmd/wad/token_cmd.go` has no audit call), and the UI path must not inherit that gap.
- **SR-116-09 No agent path to admin mutations.** No MCP tool, at any scope, mutates the
  allowlist, pairing, session or tokens. Those stay human-only, in `/admin` or the CLI.
  A prompt-injected agent must not be able to grant itself a recipient (CLAUDE.md
  §Safety 5).

| # | Asset | Threat | Control | Test (named in its slice) |
|---|---|---|---|---|
| T1 | Admin surface | Reached from the public internet, including a direct request with a forged `Host` | SR-116-02: separate loopback-only listener, bind validation, not mounted on the REST mux, TCP-peer check | `TestAdminListener_RefusesNonLoopbackBind`, `TestAdmin_NotMountedOnRESTMux`, `TestAdmin_PublicRequestWithForgedHostRejected`, `TestAdmin_NonLoopbackPeerRejected` (S5) |
| T2 | Admin token | Leaks via URL, Referer or logs | POST-only login, `Referrer-Policy: no-referrer`, log redaction | `TestAdmin_TokenNeverLogged` (S5) |
| T3 | Session cookie | Theft, fixation, cross-site send, or set over plain HTTP | SR-116-04: two accepted origins, `__Host-` + `Secure`, rotation, idle/absolute expiry | `TestAdmin_SessionCookieEstablishedOverLoopbackHTTP`, `TestAdmin_SessionCookieEstablishedViaHTTPSOrigin`, `TestAdmin_InsecureNonLoopbackOriginRejected`, `TestAdmin_SessionCookieAttributes` (S5) |
| T4 | Login, logout, every mutation | CSRF from another origin, before or after login | SR-116-03: pre-login double-submit, session synchronizer, Origin / Sec-Fetch-Site | `TestAdmin_LoginCSRFRejected`, `TestAdmin_CSRFRejected` (S5); per-mutation CSRF tests (S6, S7) |
| T5 | Pairing QR (device link) | Logged, cached, persisted, shoulder-surfed, replayed | SR-116-06: no stderr or temp file, no-store, single viewer, 60 s nonce, 3 min window | `TestAdminPair_QRNotPersisted`, `TestAdminPair_SingleViewer`, `TestAdminPair_NonceSingleUse` (S6) |
| T6 | Pairing flow | Continued after the authorising session or token is gone; a late callback renders a fresh QR | SR-116-06: cancellation triggers, dropped QR references, generation id, store revalidation/sweep | `TestAdminPair_CancelledOnSessionLogout`, `TestAdminPair_CancelledOnTokenRevoke`, `TestAdminPair_QRFetchAfterCancelIs404`, `TestAdminPair_LateCodeEventDropped` (S6) |
| T7 | Operator browser | XSS from contact names, push names or audit detail | `html/template` escaping, CSP `default-src 'self'`, no inline script or style | `TestAdmin_CSPHeader`, `TestAdmin_EscapesUntrustedNames` (S5) |
| T8 | Operator browser | Clickjacking the confirm buttons | `frame-ancestors 'none'` | `TestAdmin_CSPHeader` (S5) |
| T9 | Admin login | Token brute force | SR-116-07: per-daemon limit on TCP peer, constant-time verify | `TestAdmin_LoginRateLimited` (S5) |
| T10 | Allowlist | Prompt-injected agent grants itself a recipient | SR-116-09: no MCP mutation tools at any scope | `TestMCPScope_NoAdminMutationTools` (S2b) |
| T11 | Tokens | Escalation by a non-admin; untraceable issuance | Admin session only; secret shown once; issue/revoke audited | `TestAdminTokens_IssueShownOnceAndAudited` (S7) |
| T12 | Account link | Accidental or forged unlink | Typed-name confirm + POST + CSRF + audit | `TestAdminLogout_RequiresTypedConfirm`, `TestAdminLogout_CSRFAndAudit` (S6) |
| T13 | Health endpoint | Unauthenticated `/readyz` leaks data | `/readyz` body is reason tokens only; byte counts and timestamps only in authenticated `health` / `status` | `TestReadyz_BodyIsReasonOnly` (S1) |
| T14 | Media | Agent bulk-exfiltrates, or picks attachments by time proximity or by guessed ids | Narrowing filter required; opaque list handle bound to token and row; size cap; wrapped captions; `via:"mcp"` audit | `TestMCPMedia_RequiresNarrowingFilter`, `TestMCPMedia_HandleBindsTokenAndRow` (S3) |
| T15 | Jobs | Another token reads or cancels a job; queue exhaustion | FR-116-16: owner binding, `job_not_found` for non-owners, audited admin override, bounded table | `TestSyncJobs_CrossTokenDenied`, `TestSyncJobs_AdminOverrideAudited`, `TestSyncJobs_Bounded` (S4) |
| T16 | Admin surface | Operator misconfigures `tailscale funnel` instead of `serve` and exposes the loopback listener publicly | Residual risk, stated: the daemon cannot tell funnel from serve without trusting headers. Login (SR-116-01), the rate limit (SR-116-07) and the deferred runbook's prohibition remain | Runbook check, not a slice test |
| T17 | Loopback cookie / admin tunnel | Cookies are host-scoped, not port-scoped; another localhost service can receive the cookie; the tunnel delegates SSH/container trust | Residual risk, not fixed by `__Host-`: prefer a dedicated configured HTTPS origin; forwarder binds 127.0.0.1 only; no token in tunnel argv; keep the forwarder within the operator trust boundary | `TestAdminTunnel_LocalForwarderLoopbackOnly` (S5a), plus runbook warning; no claim of port-isolated cookies |

## 5. MCP requirements ("really good MCP")

- **FR-116-10 Typed tool errors.** Every refusal from either transport becomes a
  tool-execution error (SEP-1303) whose structured content is

  ```json
  {"code": -32117, "reason": "message_not_found",
   "remediation": "No stored message has this id. List candidates with wa_get_thread or wa_list_media."}
  ```

  A `required_scope` field is added only when `reason` is `scope_insufficient`. `reason`
  is a closed enum carried as **`error.data.reason` on every RPCWire error row**,
  including generic/internal rows; it is not inferred from code or message. Generate the
  reason catalog from `app.RPCWire` and `internal/app/errors.go`, one row per sentinel/
  refusal, extending it for catalog entries as needed: `message_not_found`,
  `media_not_found`, `media_not_cached`, `media_unsupported`, `media_too_large`,
  `not_allowlisted`, `policy_refused`, `scope_insufficient`, `not_paired`,
  `session_wiped`, `disconnected`, `rate_limited`, `warmup_active`, `invalid_params`,
  `storage_unwritable`, `job_not_found`, `timed_out`, `capacity_exceeded`,
  `upstream_error`, `internal`. REST-generated refusals follow the same data contract.
  Socket serialization and the stdio caller preserve error data.

  Both MCP bridges (stdio/socket and Streamable HTTP) map **only from `data.reason`**;
  missing/unknown reasons map to `internal`, with no code/text fallback. Codes collide:
  `-32100` covers not-allowlisted and other policy refusals (`rpcwire.go:63-74`),
  and `-32099` covers scope and media refusals (F3). S2a tests both collisions on both
  transports, with misleading messages plus absent/unknown reasons.

  For `not_allowlisted` the body stays constant, preserving FR-050 (no recipient echo),
  and the remediation names the human action ("ask the operator to grant it in
  /admin/allowlist or with `wa allow add`; do not retry until they confirm"). Both
  transports get the code from `app.RPCWire`. `dispatcherCaller`'s private mapping
  (`cmd/wad/mcp_http.go:43-50`) and the numeric bands (`internal/mcpbridge/errors.go:12-27`)
  are replaced. Closes F1–F3 on MCP.
- **FR-116-11 Scope-filtered tool list.** The tool set is a pure function of the caller's
  scope: `read` gets read tools; `send` gets read plus send tools under the configured
  send mode; `admin` adds admin-only read tools (`wa_audit_tail`) and the scheduling
  mutation `wa_schedule_message` (`schedule.*` are admin in `MethodScopes`). Each
  tool's description and `_meta` state its required scope. Stdio maps `--read-only` to
  `read` and the default to `send`. Property: for every scope s and every listed tool t,
  `required_scope(t) ≤ s`, and calling an unlisted tool name is "unknown tool", never a
  scope error.
- **FR-116-12 Scope introspection.** A read-scope method `auth.whoami` (MCP: part of
  `wa_status`) returns the held scope and, for a given method name, its required scope.
  It is derived from `rest.RequiredScope` (`internal/adapters/primary/rest/scope.go:206`)
  so it cannot drift. Closes F3 before the fact.
- **FR-116-13 Truthful `wa_status`.** Returns `health` and `reasons` exactly as §6's truth
  table defines them, plus the S1 fields, and the account number masked to its last four
  digits, never the full JID.
- **FR-116-14 `wa_allowlist_check`.** Read scope. Input: recipient (name or number,
  resolved like `wa_resolve_contact`) and action (`send`, `read`, `group.add`,
  `group.create`). Output: `{allowed: bool, action, remediation?}`. It reads the in-memory
  allowlist without mutating it, is audited as a decision record, and is rate-limited with
  the read surface. It does not reveal which other actions or recipients are allowlisted.
  Backed by a new `allow.check` method classified `ScopeRead`; `allow` op=list stays admin.
- **FR-116-15 Media read tools.**
  - `wa_list_media {chat, sender?, caption?, media_type?, since?, until?, limit≤50}`
    wraps `media.list`. `chat` is required, **and** at least one of `sender`/`caption` is
    required. A caption-only filter needs at least 3 Unicode code points after trimming
    whitespace; empty/short captions do not qualify (return `invalid_params` unless a
    sender already supplies the required narrowing). `since`/`until` only narrow a request
    that already has those filters,
    so the tool cannot select attachments by timestamp proximity. The result echoes
    `appliedFilters` (`cmd/wad/media_list_method.go:58`). Inbound captions stay inside the
    channel envelope.
  - *How a fetch proves it came from a list.* Each listed row carries an opaque
    `media_handle = HMAC(per-process key, encode(principal, chat, message id, expiry))`.
    The encoding is domain-separated (`wa-media-v1`); each UTF-8 field (expiry as decimal
    Unix seconds) is prefixed by its unsigned 32-bit big-endian byte length, not ambiguous
    concatenation. Handles last 15 min and are void after a daemon restart; on stdio the key belongs to the
    `wa mcp serve` process, so its handles live for that process instead. `wa_get_media {media_handle, max_bytes?}`
    accepts only a handle, never raw ids. A handle minted for another token, expired, or
    forged is `invalid_params`. Matching an arbitrary database row is not enough.
  - `wa_get_media` size caps: default 5 MiB, hard cap 16 MiB (below
    `domain.MaxInboundMediaBytes`). Over the cap it returns `media_too_large` with the
    object's size and the operator CLI command, and no bytes. Bytes go out as an MCP
    embedded resource, through `media.fetchBytes` chunks on stdio (socket frame cap,
    `internal/app/method_media.go:141-152`).
  - Both tools are read scope, toolset `messages`, audited with `via:"mcp"`. S3 itself
    emits FR-116-17’s `_at` / `_unix_s` pairs and wrapped captions on its new results,
    translating existing RPC fields; it does not wait for S8.
- **FR-116-16 No call blocks over 10 s; jobs are owned.**
  - *Deadline table.* A new `MethodDeadlines` table classifies every `MethodScopes` key
    (`internal/adapters/primary/rest/scope.go`), with the same fail-closed rule (an
    unclassified method fails its test). A method is either `bounded`, meaning the
    dispatcher enforces a 10 s **return** deadline on every transport (socket, REST,
    both MCP transports), or `job`. There is no exempt class. A context deadline alone
    is not enforcement: the dispatch boundary must race completion against the deadline
    without waiting for a handler to acknowledge cancellation. The byte-stream HTTP routes
    (`GET /media/{sha256}`, `POST /media/upload`, `GET /v1/events`) are not RPC methods
    and keep the server's own Read/WriteTimeout.
  - *Job-capable methods:* `sync.force`, `media.download`, `pair`, and every other
    handler whose work detaches from its request context. `async:true` returns
    `{job_id, state:"queued"}` at once. With `async` omitted or false, a result completed
    before 10 s keeps its synchronous shape; otherwise the dispatch boundary returns
    `{job_id, state:"running", timed_out:true}` **at the deadline**, without joining the
    worker. The work continues detached and reports its terminal result/error through
    that same job. `sync.force` carries FR-116-25's `gap_reachable` and reason in its
    synchronous response, initial job response and final result (per chat for a sweep);
    undetermined reachability is false with `gap_state_unknown`, never optimistic success.
    It may also report `received`; late history still persists. The daemon **never
    re-executes on its own** on detach, timeout, polling or cancellation. A client retry
    is a new execution, subject only to an explicitly supplied existing idempotency key;
    no automatic deduplication is promised. Send-class remediation says
    **"poll job_id, do not resend"**. `wait` clamps `timeoutMs` to 10 000. S4 updates `wa pair` to poll
    its job id without starting pair again, and `wa wait` to use bounded polls within
    its caller's overall timeout/cancellation budget.
  - *Detached-context inventory and cancellation.* `Pair` currently ignores the caller
    and creates a fresh 3 min context (`internal/adapters/secondary/whatsmeow/pair.go:100-110`).
    S4 records each detaching handler in `MethodDeadlines`, with its worker cancellation
    hook and maximum worker lifetime. Pair's context instead derives from the owned job
    context (not a fresh `Background`); its 3 min limit remains. Pair cancellation also
    applies SR-116-06's QR invalidation, generation check and late-success audit; that part
    lands with S6a, so S6 depends on S4c (S4 itself only derives, inventories and cancels
    Pair's context). Other
    detached handlers must propagate jobs.cancel, token revocation and daemon shutdown
    into their worker and all child operations. A method cannot become `bounded` merely
    by accepting `context.Context`; S4's coverage test checks this inventory.
  - *Separate admission pools.* Ordinary synchronous executions use a larger in-flight
    pool (64 per daemon / 16 per principal), not job quota. Allocate one random execution
    id before dispatch; refuse excess admission before effects. `panic` and `session.logout` are
    never refused at executor saturation, so the operator can always stop or unlink. Only explicit async work
    and deadline-promoted work use the job caps: **16 per daemon / 2 per owner**. Fast
    synchronous completion releases its in-flight slot without consuming job capacity;
    8 concurrent fast calls by one principal must succeed. Async checks capacity before
    effects; promotion atomically transfers the same execution, never invokes it again.
    If promotion finds job quota full, return the existing id at the deadline with
    `state:"capacity_waiting"`: the already-running worker stays charged to in-flight
    capacity and pollable/cancellable there until transfer or completion. This is not an
    additional admitted job, and starts no worker. Its completed result retains that
    in-flight entry until fetched or the 1 h expiry, bounding retained overflow results.
  - *Cancellation-resistant counterexample.* Even a handler that ignores cancellation
    returns at 10 s with its existing execution id as `job_id`, including while awaiting
    promotion quota. Cancellation signals but never joins the worker; retain its occupied
    pool slot until exit, suppress unauthorised late results and never start a replacement.
    Cancellation cannot undo external effects. A fake finishing at 30 s must have a
    pollable id at 10 s and one eventual result; release all fake workers. Cover full
    promotion quota too, with no leaked worker or unaccounted execution.
  - *Job lifecycle.* `jobs.get` returns `capacity_waiting | queued | running |
    cancellation_requested | done | failed | cancelled | expired`, with any authorised
    result; `jobs.cancel` cancels. Ids are 128-bit random; worker contexts derive from
    the daemon, not the request. Both pools are bounded, cancelled at shutdown, and retain
    results for at most 1 h after completion.
  - *Authorisation.* Owner is the token id, `env` under single-token auth (implicit
    admin), or the **socket principal** `socket:<daemon-profile>`: a shared local operator,
    admin scope, authenticated by the profile's owner-only Unix socket and stable across
    connections. It owns socket-started executions and may get/cancel them; token-owned
    work requires the explicit audited admin override. Stdio MCP keeps its read/send
    tool restrictions (FR-116-11), never admin mutation tools through socket privilege.
    A non-owner below admin receives `job_not_found`, identical to an unknown id. Admin
    override is audited as `job_admin_access`. Every token-backed get/cancel revalidates
    caller and backing owner directly in the store; no override returns revoked-owner
    results. The ≤30 s sweep cancels externally revoked/expired owners' jobs without
    polling (SR-116-04). Get is read scope; cancel needs the initiating method's scope.
    Ownership, revalidation and result rules apply equally to both pools.
  - *MCP.* Adds `wa_sync_history` (send scope) and `wa_job_status` (read scope).
    `wa_wait_for_reply` clamps each call to 10 s and returns `{timed_out:true}` for the
    agent to re-poll, instead of 60–300 s (`internal/mcpbridge/tools.go:205-211`).
- **FR-116-17 Unambiguous time and caption fields.** Every MCP result carries time as a
  pair: `<name>_at` (RFC 3339, UTC) and `<name>_unix_s`. A bare `timestamp` never appears
  in a new field. Each message gets explicit `has_caption: bool` and `caption`, where the
  inbound `caption` is its own FR-005a envelope (the existing `channel` field is kept for
  compatibility), so a consumer never finds a caption by parsing `channel`.
- **FR-116-18 One eval case per F-class** in `internal/mcpbridge/evals/evals_test.go`
  (the `TestEvalSet` table): `f1-missing-message-is-not-found`,
  `f2-preflight-before-send`, `f3-scope-shown-before-call`, `f4-status-says-not-paired`,
  `f5-status-says-storage-down`, `f6-sync-returns-job-id`,
  `f7-time-fields-carry-units`, `f9-recovery-defers-until-live-anchor`,
  `f10-tail-gap-is-unreachable`, plus `media-list-requires-sender-or-caption`.
  `tools.snapshot.json` is updated in the same slice as each tool change.

## 6. Health requirements

- **FR-116-20 Fields.** `health` and `status` add:
  - `storage` (`ok | unknown | failed`);
  - `last_persist_failure_at` / `_unix_s`;
  - `persist_failures_total` and separate `persist_logic_errors_total`;
  - `free_bytes` (data-dir filesystem);
  - `last_received_at` / `_unix_s`;
  - `last_persisted_at` / `_unix_s`;
  - `ingest_lag_s`;
  - `reasons` (§6 table).

  All are additive under `omitempty` (no schema v2 bump). Today's `lastEventTs` stays as
  a compatibility alias of `last_received_unix_s`.
- **FR-116-21 Storage truth comes from the database.**
  - *Probe table.* S1 adds migration v9 to `backedUpSteps`
    (`internal/adapters/secondary/sqlitehistory/migrate.go`, after `{8, migrateV8}`), so
    it gets the backup and `migration_history` row like every step. It creates
    `health_probe (id INTEGER PRIMARY KEY CHECK (id = 1), wrote_at INTEGER NOT NULL)` in
    `messages.db`.
  - *Probe loop and idle startup.* Off the request path, run immediately at startup and
    every `WAD_STORAGE_PROBE_SEC` (default 30 s), with a 5 s transaction timeout. While
    `storage != ok` (unknown or failed), the loop itself runs the **recovery transaction**:
    `BEGIN IMMEDIATE`, the same messages-table insert SQL **including indexes/triggers**,
    a reserved local fixture ≥ `max(largest failed record size, 64 KiB)`, delete that
    fixture, update `health_probe`, commit. Use the history store pool/WAL write path.
    Emit **no events/webhooks**, update no receipt watermarks and expose no fixture as
    history or protocol anchor. Retain the failed-size high-water mark across restart;
    without prior size use the 64 KiB floor. Recovery counts only if it **started after
    the latest failure**. An idle paired/connected account can thus become ready without
    a live inbound message. While storage is ok the loop may use a tiny upsert for probe
    freshness, but a tiny upsert can never clear a latch or initialise storage ok.
  - *Error classes.* Only storage-class failures latch: `SQLITE_FULL`, `SQLITE_IOERR`,
    `SQLITE_READONLY`, `SQLITE_CANTOPEN`, `SQLITE_CORRUPT` (including extended codes)
    and `SQLITE_BUSY` persisting after the 5 s timeout. They set storage failed, update
    `last_persist_failure_at` and increment `persist_failures_total`. Transient BUSY
    retries within the timeout. Constraint/logic errors increment the separate
    `persist_logic_errors_total` and surface diagnostically, but neither latch storage
    failure nor prove recovery. Missing probe progress still fails freshness closed.
  - *Real writes and state machine.* Storage starts unknown/unready. Results from
    `InsertRawInteractive` (`adapter_inbound.go:143`) feed that classifier. Only a
    successful real message-table write or the loop's representative transaction makes
    it ok. A storage failure remains **latched** until a qualifying write that began
    after the latest failure commits; a monotonic write-start sequence orders this, never
    a timer. Duplicate/no-op inserts and tiny probe upserts do not clear it. Recovery
    failure is unready, never implicit success. Probe freshness remains independent: a
    qualifying real insert cannot hide stale loop progress.
  - *B4 counterexample.* Given a message insert fails with `SQLITE_FULL`, when the
    `health_probe` upsert succeeds but the next message insert still fails `SQLITE_FULL`,
    then `storage=failed` and `/readyz` stays **503**. Advancing the fake clock past
    five minutes and continuing successful upserts must not change that result. Only
    a later qualifying messages-table commit can clear the latch, with a fresh probe.
  - *Stale probe.* If the last conclusive probe is older than 2 × the interval (60 s at
    defaults), `storage` is reported as `failed`, with detail `probe_stale`.
  - *Detection bound.* A failed real insert is visible on the next health read. A
    database that silently stops accepting writes is visible within one interval plus the
    probe timeout (≤ 35 s at defaults). A hung probe is caught within 2 × interval (60 s).
  - The history store is absent only in test compositions, and there the storage
    reasons are omitted.
- **FR-116-22 Ingest lag.**
  - *Clocks.* `last_received_at` is the daemon's wall clock when the event handler took
    the message, and `last_persisted_at` is the daemon's wall clock when its insert
    committed. Sender message timestamps are never used for health.
  - *Duplicates.* An insert absorbed by `ON CONFLICT DO NOTHING` counts as persisted
    for lag, because the row is present; it cannot clear a storage failure latch because
    it proves no new message-table write (FR-116-21).
  - *Lag.* `ingest_lag_s` is now minus the receipt time of the **oldest** message
    received whose insert has not yet finished, or 0 when none is in flight. Tracking the
    oldest pending message, not the newest completed one, means out-of-order completion
    cannot shrink it.
  - *Failures.* A failed insert is not "pending". Storage-class failures latch per
    FR-116-21 and stay counted in `persist_failures_total` / `last_persist_failure_at`;
    constraint/logic failures use their separate counter. Both retain receipt-failure
    bookkeeping so readiness does not imply data recovery. A newer successful insert can clear
    readiness only under FR-116-21's qualifying-write and probe-freshness rules; it cannot
    hide that the older one failed. Recovery of writability is not recovery of the lost
    message window: that remains tracked by FR-116-24.
  - *Idle accounts.* An idle account has nothing in flight, so its lag is 0 and it does
    not flap.
  - `ingest_stale` holds when `ingest_lag_s > WAD_INGEST_STALE_SEC` (default 300) while
    connected.
- **FR-116-23 One severity and readiness truth table.**

  | Reason | Holds when | Severity | `/readyz` |
  |---|---|---|---|
  | `not_paired` | no session | `down` | 503 |
  | `storage_unknown` | `storage = unknown` (startup, before the first qualifying write or recovery transaction) | `down` | 503 |
  | `storage_unwritable` | `storage = failed`, including `probe_stale` | `down` | 503 |
  | `disconnected` | session present, transport not connected (live state, not session existence) | `degraded` | 503 |
  | `ingest_stale` | FR-116-22 | `degraded` | 503 |
  | `soft_stale` | 110g stale state (connected, no inbound past threshold) | `degraded` | **200** |
  | `storage_low` | `free_bytes < WAD_MIN_FREE_BYTES` (default 64 MiB) while `storage = ok` | `degraded` | **200** |

  - `health` = the highest severity among the reasons that hold (`down > degraded >
    healthy`), and `healthy` iff none hold.
  - **`/readyz` is 200 iff no reason in the 503 column holds.** `soft_stale` and
    `storage_low` are explicit warnings: they make `health` `degraded` while readiness
    stays 200. Soft-stale is a quiet-account signal that 110g already surfaces, and
    low-but-writable space still persists.
  - The 503 body is one reason token per line, nothing else. `/readyz` reads only cached
    state and stays sub-millisecond (`cmd/wad/health_http.go:30-32`).
  - `IsReady` stops being session existence (`internal/app/method_health.go:135`), and
    `status.connected` stops being session existence (`internal/app/method_status.go:49`).
  - `/healthz` liveness is unchanged (spec 109 FR-004). Dokku's zero-downtime check uses
    `/healthz` (`docs/deploy/dokku.md:137`), so a readiness flip never restarts a
    container (R27: no auto-act on the new signal).

- **FR-116-24 Deferred recovery backfill (F9).**
  - A `storage_unwritable → writable` transition, or daemon start with
    `last_persisted < last_received`, arms a pending recovery episode per affected chat.
    These comparisons use receipt/persistence bookkeeping, not sender timestamps;
    retain unresolved receipt failures separately so a newer commit cannot hide an
    older missing receipt across restart.
    Recovery markers (outage start, per-chat pending state and receipt watermarks) survive
    restart; an unclean restart with missing/unwritable markers is `recovery_unknown`,
    not evidence of no gap, and conservatively arms known chats. A failed watermark write
    must not turn loss of evidence into a healthy recovery claim.
    A `recovery_unknown` episode has no receipts to compare, so it exits after **one
    delivered pull from its own real post-recovery anchor**: the episode clears and the
    result is recorded and logged as unverified, never as a verified recovery.
    Reachability (FR-116-25) is decided per chat from that chat's own episode; an unknown
    episode in another chat never makes a chat without an episode `gap_state_unknown`.
  - Arming alone sends no request. The **first live message persisted after recovery**
    in each pending chat supplies a real stored anchor. If that same commit changes
    storage to writable, arm then consume the episode with that message atomically; do
    not wait for a second live message. Trigger one deferred pull of up to 50 messages
    **before that exact message**, not before the pre-outage newest row.
    Failed inserts, duplicate/no-op inserts, replayed history and unrelated chats cannot
    trigger it. Concurrent live arrivals consume the same episode once, not once each.
  - Scheduling is opt-in (`WAD_STORAGE_RECOVERY_BACKFILL`, default false/detect-only),
    edge-triggered and cooldown-bounded to one attempt per chat per 5 min. The pending
    state is always visible; when enabled, the first qualifying live commit triggers
    the pull (or queues it until cooldown expires). A daemon-wide budget allows at most
    **2 concurrent pulls and 10 starts per rolling minute**, across watchdog, recovery
    and explicit force sweeps. Excess waits in a fair per-chat queue capped at 64 entries;
    overflow remains visibly pending. Keep the original real anchor while queued; new
    arrivals do not mint more pulls. No reconnect, logout or session
    mutation occurs. Failure remains visible as pending/failed; no retry loop or fabricated
    success. An anchored episode whose pull did not deliver re-fires with its original
    anchor on the next qualifying live commit in that chat after its cooldown expires,
    and once at daemon start when restored anchored but undelivered. A 50-message cap is not a promise to recover an arbitrarily deep outage.
    Cancellation/shutdown leaves the unresolved episode visible on restart.
  - Given a storage outage then recovery with no live message yet, no pull occurs. Given
    the first subsequent live commit, exactly one pull uses that message's real anchor.
    Property: readiness recovery never deletes the pending gap; only measured backfill
    results update recovery status. Fake-clock tests cover both transition and restart,
    edge/cooldown, opt-in, duplicates, a failed first live insert, the unknown-episode
    exit and the post-cooldown and restart re-fire.
- **FR-116-25 Honest gap reachability (F10).** `sync.force` and its job/MCP result
  expose `gap_reachable` plus a machine `reason`. When the chat's newest stored anchor
  predates the outage window, return `gap_reachable:false`,
  `reason:"tail_gap_unanchored"`, `requested:false` instead of a silent "Requested".
  With no real anchor, the reason is `no_anchor`; missing recovery evidence is
  `gap_state_unknown`, never proof of recovery. A global sweep reports reachability
  per chat, not a single optimistic success. Explicit `older_history:true` (CLI
  `--older-history`) may request preceding history despite false reachability; only this
  flag permits `requested:true` in that case, with the same reason. The default performs
  no useless pull; older history is never labelled gap recovery. Once a real post-recovery live anchor exists,
  `gap_reachable:true` means the pull can address the window behind it, **not** that all
  missing messages were restored; report actual results and the 50-message cap separately.
  Given a tail gap and an older newest row, a fake-clock test observes false without a
  network pull; after a real live commit, it observes that exact anchor and one pull.
  Property: request/ACK success alone never implies a gap is reachable or recovered.

## 7. Slices

Each slice is one PR of about 400 changed lines or fewer and touches no `specs/` file
(spec-guard). Any slice that adds a state-changing route ships that route's CSRF test and
audit test in the same PR, so security never trails a feature. The test names in each
block are the contract: the slice adds them, and the block proves each one ran and
passed.

Order: S1 goes first because S2b's `wa_status` and S5's page read the S1 fields, and a
page built on the baseline `IsReady` would display the F5 lie. S1b follows S1 immediately
so truthful readiness is paired with truthful gap recovery before the MCP/UI slices.

**Predeclared splits.** If a slice exceeds the line budget it splits only as declared
here, and each part's acceptance is the parent block restricted to its own test names
(the union of the parts must equal the parent's list):

| Slice | Part a | Part b | Part c |
|---|---|---|---|
| S2 | errors | scope list + `allow.check` (already S2a/S2b below) | — |
| S4 | engine + deadline table/classification (dark) | ownership (dark) | transport wiring + job-capable methods + MCP tools + CLI |
| S5 | listener + tunnel + login/session/CSRF/audit + CSP/frame-ancestors + escaping | status page only | — |
| S6 | pairing | logout | — |
| S7 | allowlist | tokens + audit tail | — |

S4 splits three ways because every `main` merge deploys. S4a and S4b are **dark**: they are
built and tested at the app/dispatcher layer only, with no transport-visible change (not
wired into the socket, REST or either MCP transport). S4c wires the engine into every
transport, makes `sync.force` and `media.download` job-capable, and ships the MCP tools and
CLI polling together, so ownership is live the moment jobs are reachable. S4c is cut in two
(table below): S4c1 needs only S4b, and S4c2 lands after S1b (FR-116-25 `gap_reachable`). Each part's acceptance is the S4 block restricted to its
own names. S4a: `TestMethodDeadlines_EveryMethodClassified`, `TestNoMethodBlocksOver10s`,
`TestMethodDeadline_IgnoresCancellationReturnsJob`, `TestMethodDeadline_CancelRetainsWorkerSlot`,
`TestMethodDeadline_InFlightSeparateFromJobCaps`, `TestMethodDeadline_PromotionFullRemainsBounded`,
`TestMethodDeadline_NoSelfReexecution`, `TestSyncJobs_Bounded`, `TestSyncJobs_Cancel`,
`TestSyncJobs_ReaperNoLeak`. S4b: `TestSyncJobs_CrossTokenDenied`,
`TestSyncJobs_AdminOverrideAudited`, `TestSyncJobs_CancelledOnTokenRevoke`,
`TestSyncJobs_SocketPrincipal`, `TestSyncJobs_DirectStoreRevokeAndExpiry`. S4c:
`TestMethodDeadlines_DetachedInventoryAndCancellation`, `TestSyncForce_SyncModeDeadline`,
`TestMediaDownload_SyncModeDeadline`, `TestSyncForce_JobCarriesGapReachability`,
`TestWaitForReply_Clamped`, `TestCLI_PairPollsWithoutRestart`,
`TestCLI_WaitHonoursOverallDeadline`, `TestToolSnapshot`, `TestEvalSet/f6-sync-returns-job-id`.

| Part | Scope | Restricted names | After |
|---|---|---|---|
| S4c1 | engine wired into all 4 transports with principals; `jobs.get`/`jobs.cancel`; Pair ctx + detached inventory; wait clamps; `wa_job_status`; CLI pair/wait polling | `TestMethodDeadlines_DetachedInventoryAndCancellation`, `TestMediaDownload_SyncModeDeadline`, `TestWaitForReply_Clamped`, `TestCLI_PairPollsWithoutRestart`, `TestCLI_WaitHonoursOverallDeadline`, `TestToolSnapshot` | S4b |
| S4c2 | job-capable `sync.force` with `gap_reachable`/`reason`; `wa_sync_history` | `TestSyncForce_SyncModeDeadline`, `TestSyncForce_JobCarriesGapReachability`, `TestEvalSet/f6-sync-returns-job-id` | S4c1, S1b |

Part a always carries the security controls its routes need.

**Acceptance harness (lands in S1).** S1 adds `scripts/assert-tests-ran.py`. It reads a
`go test -json` stream and exits non-zero unless every named test (subtests as
`Parent/sub`) has an `"Action":"pass"` event and no event in the stream has
`"Action":"fail"`. A `-run` selector that matches nothing therefore fails the block,
because its names never pass. Every block below:

- starts `set -euo pipefail`, resolves the repo top level and asserts HEAD equals
  `PR_HEAD_SHA` supplied by the caller (the implementation PR head, not the composed
  GitButler workspace); run future slice checks in a frozen clean checkout;
- runs `go test -json … | tee` into a temp file (pipefail propagates a failing
  `go test`); an `ERR` trap then calls `showfail`, which prints the failing command
  and the `fail` and `build-output`/`build-fail` events (plus each failing test's
  `--- FAIL`, `_test.go:N:` and `panic:` lines) from that file to stderr, and
  `set -e` still exits non-zero, so a failure is never silent;
- calls the harness with the full contracted list; and
- for file checks, separates "absent" from "clean": `grep` exit 1 is clean, 0 is a
  match, 2 is an error.

All Go commands run with `GOMAXPROCS=2 GOFLAGS=-p=2` (the desktop cgroup refuses default
parallelism).

#### S1 — health truthfulness (F5)

FR-116-20..23; the `status.connected` / `IsReady` fix; `wa_status` health and masking
(FR-116-13); migration v9; the acceptance harness. The truth-table test enumerates all
2⁷ reason combinations. Probe tests use `testing/synctest` and a fault-injecting SQLite
fake (full, read-only, busy) for the database-only failure, with the filesystem left
writable. `TestStorageProbe_UpsertSucceedsMessageInsertFullStays503` is the B4
counterexample: a successful probe upsert plus another message insert failing
`SQLITE_FULL` stays 503, including after five fake minutes.

Acceptance:

```bash
set -euo pipefail
cd "$(git rev-parse --show-toplevel)"
test "$(git rev-parse HEAD)" = "${PR_HEAD_SHA:?set to the tested PR head SHA}"
export GOMAXPROCS=2 GOFLAGS=-p=2
go build ./...
go vet ./...
test -f scripts/assert-tests-ran.py
# the harness must refuse a stream in which the contracted test never ran
if python3 scripts/assert-tests-ran.py /dev/null TestNeverRan; then echo "harness passed an empty stream"; exit 1; fi
OUT=$(mktemp)
showfail() { echo "FAILED: $1; fail/build events from $OUT:" >&2; jq -rR '(fromjson? // {Action:"raw",Output:.}) | select(.Action=="fail" or .Action=="build-fail" or .Action=="raw" or .Action=="build-output" or (.Action=="output" and ((.Test==null and (.Output|test("^(PASS|ok\\s|-test\\.shuffle)")|not)) or (.Output|test("^\\s*--- FAIL|_test\\.go:[0-9]+:|panic:"))))) | if (.Action|test("output|raw")) then (.Output|rtrimstr("\n")) else "\(.Action) \(.Package // .ImportPath // "") \(.Test // "")" end' "$OUT" >&2 || true; }
trap 'showfail "$BASH_COMMAND"' ERR
go test -json -race -shuffle=on -count=1 -run '^(TestHealthTruthTable_AllReasonCombinations|TestStorageProbe_StartsUnknownAndUnready|TestStorageProbe_FailureLatchesPastFiveMinutes|TestStorageProbe_DatabaseOnlyFailure|TestStorageProbe_RecoversOnlyAfterLaterWrite|TestStorageProbe_UpsertSucceedsMessageInsertFullStays503|TestStorageProbe_StaleProbeCountsAsFailure|TestStorageProbe_BusyAfterTimeoutLatches|TestIngestLag_NewerInsertDoesNotHideOlderFailure|TestIngestLag_IdleAccountStaysReady|TestStatus_ConnectedIsLive|TestStatus_AccountMaskedToLastFour|TestReadyz_BodyIsReasonOnly|TestReadyz_SoftStaleStays200|TestMigrateV9_HealthProbeTable|TestStorageProbe_IdleRestartBecomesReady|TestStorageProbe_DuplicateDoesNotClearLatch|TestStorageProbe_ConstraintDoesNotLatch|TestStorageProbe_RecoveryFixtureFloorAndNoEvents)$' \
  ./internal/app/ ./cmd/wad/ ./internal/adapters/secondary/sqlitehistory/ | tee "$OUT" >/dev/null
go test -json -count=1 -run '^TestEvalSet$/^(f4-status-says-not-paired|f5-status-says-storage-down)$' ./internal/mcpbridge/evals/ | tee -a "$OUT" >/dev/null
python3 scripts/assert-tests-ran.py "$OUT" \
  TestHealthTruthTable_AllReasonCombinations TestStorageProbe_StartsUnknownAndUnready \
  TestStorageProbe_FailureLatchesPastFiveMinutes TestStorageProbe_DatabaseOnlyFailure \
  TestStorageProbe_RecoversOnlyAfterLaterWrite TestStorageProbe_UpsertSucceedsMessageInsertFullStays503 \
  TestStorageProbe_StaleProbeCountsAsFailure \
  TestStorageProbe_BusyAfterTimeoutLatches TestIngestLag_NewerInsertDoesNotHideOlderFailure \
  TestIngestLag_IdleAccountStaysReady TestStatus_ConnectedIsLive TestStatus_AccountMaskedToLastFour \
  TestReadyz_BodyIsReasonOnly TestReadyz_SoftStaleStays200 TestMigrateV9_HealthProbeTable \
  TestEvalSet/f4-status-says-not-paired TestEvalSet/f5-status-says-storage-down \
  TestStorageProbe_IdleRestartBecomesReady TestStorageProbe_DuplicateDoesNotClearLatch TestStorageProbe_ConstraintDoesNotLatch TestStorageProbe_RecoveryFixtureFloorAndNoEvents
```

#### S1b — deferred backfill and honest tail-gap reachability (F9, F10)

FR-116-24/25: durable recovery markers, storage recovery/startup arming, per-chat
first-live-persist anchor, one capped pull, opt-in/cooldown and unresolved-gap status.
Every time-sensitive test uses `testing/synctest` with an in-memory fake history peer;
no live daemon, real network or synthetic/future anchor. The restart test restores only
local recovery bookkeeping into a fresh fake daemon, not a live session. Add goleak
coverage for the worker. If the slice exceeds the line budget, S1b-a carries markers,
arming and their tests; S1b-b carries the pull/reachability and remaining tests, in that
order with the full parent test union retained.

Acceptance:

```bash
set -euo pipefail
cd "$(git rev-parse --show-toplevel)"
test "$(git rev-parse HEAD)" = "${PR_HEAD_SHA:?set to the tested PR head SHA}"
export GOMAXPROCS=2 GOFLAGS=-p=2
go build ./...
go vet ./...
OUT=$(mktemp)
showfail() { echo "FAILED: $1; fail/build events from $OUT:" >&2; jq -rR '(fromjson? // {Action:"raw",Output:.}) | select(.Action=="fail" or .Action=="build-fail" or .Action=="raw" or .Action=="build-output" or (.Action=="output" and ((.Test==null and (.Output|test("^(PASS|ok\\s|-test\\.shuffle)")|not)) or (.Output|test("^\\s*--- FAIL|_test\\.go:[0-9]+:|panic:"))))) | if (.Action|test("output|raw")) then (.Output|rtrimstr("\n")) else "\(.Action) \(.Package // .ImportPath // "") \(.Test // "")" end' "$OUT" >&2 || true; }
trap 'showfail "$BASH_COMMAND"' ERR
go test -json -race -shuffle=on -count=1 -run '^(TestDeferredBackfill_StorageRecoveryArmsUntilFirstLivePersist|TestDeferredBackfill_RestartWithUnpersistedReceiptsArms|TestDeferredBackfill_UnknownRestartFailsClosed|TestDeferredBackfill_FailedInsertDoesNotTrigger|TestDeferredBackfill_FirstLiveAnchorExactlyOnce|TestDeferredBackfill_OptInEdgeAndCooldown|TestDeferredBackfill_CappedAt50|TestDeferredBackfill_WorkerNoLeak|TestForceSync_TailGapUnreachable|TestForceSync_RealPostRecoveryAnchor|TestForceSync_GlobalReportsPerChatReachability|TestDeferredBackfill_DaemonBudgetAndFairQueue|TestForceSync_ExplicitOlderHistoryFlag)$' \
  ./cmd/wad/ ./internal/adapters/secondary/whatsmeow/ ./internal/adapters/secondary/sqlitehistory/ | tee "$OUT" >/dev/null
go test -json -count=1 -run '^TestEvalSet$/^(f9-recovery-defers-until-live-anchor|f10-tail-gap-is-unreachable)$' ./internal/mcpbridge/evals/ | tee -a "$OUT" >/dev/null
python3 scripts/assert-tests-ran.py "$OUT" \
  TestDeferredBackfill_StorageRecoveryArmsUntilFirstLivePersist TestDeferredBackfill_RestartWithUnpersistedReceiptsArms \
  TestDeferredBackfill_UnknownRestartFailsClosed TestDeferredBackfill_FailedInsertDoesNotTrigger \
  TestDeferredBackfill_FirstLiveAnchorExactlyOnce TestDeferredBackfill_OptInEdgeAndCooldown \
  TestDeferredBackfill_CappedAt50 TestDeferredBackfill_WorkerNoLeak TestForceSync_TailGapUnreachable \
  TestForceSync_RealPostRecoveryAnchor TestForceSync_GlobalReportsPerChatReachability \
  TestEvalSet/f9-recovery-defers-until-live-anchor TestEvalSet/f10-tail-gap-is-unreachable \
  TestDeferredBackfill_DaemonBudgetAndFairQueue TestForceSync_ExplicitOlderHistoryFlag
```

#### S2a — typed MCP/RPC errors (F1, F2 wire half)

FR-116-10 on both MCP transports, through `app.RPCWire`. The REST `403` adds
`{reason:"scope_insufficient", required_scope, granted_scope}` to the problem body and
keeps the wire code.

Acceptance:

```bash
set -euo pipefail
cd "$(git rev-parse --show-toplevel)"
test "$(git rev-parse HEAD)" = "${PR_HEAD_SHA:?set to the tested PR head SHA}"
export GOMAXPROCS=2 GOFLAGS=-p=2
go build ./...
go vet ./...
OUT=$(mktemp)
showfail() { echo "FAILED: $1; fail/build events from $OUT:" >&2; jq -rR '(fromjson? // {Action:"raw",Output:.}) | select(.Action=="fail" or .Action=="build-fail" or .Action=="raw" or .Action=="build-output" or (.Action=="output" and ((.Test==null and (.Output|test("^(PASS|ok\\s|-test\\.shuffle)")|not)) or (.Output|test("^\\s*--- FAIL|_test\\.go:[0-9]+:|panic:"))))) | if (.Action|test("output|raw")) then (.Output|rtrimstr("\n")) else "\(.Action) \(.Package // .ImportPath // "") \(.Test // "")" end' "$OUT" >&2 || true; }
trap 'showfail "$BASH_COMMAND"' ERR
go test -json -race -shuffle=on -count=1 -run '^(TestRemediate_EveryCatalogCode|TestRemediate_NoNumericBands|TestRemediate_NotAllowlistedBodyConstant|TestDispatcherCaller_UsesRPCWire|TestScopeRefusal_NamesRequiredScope|TestRemediate_CodeCollisionsBothTransports|TestRemediate_MissingUnknownReasonIsInternal|TestRPCWire_EveryRowCarriesReason)$' \
  ./internal/mcpbridge/ ./cmd/wad/ ./internal/adapters/primary/rest/ | tee "$OUT" >/dev/null
go test -json -count=1 -run '^TestEvalSet$/^f1-missing-message-is-not-found$' ./internal/mcpbridge/evals/ | tee -a "$OUT" >/dev/null
python3 scripts/assert-tests-ran.py "$OUT" \
  TestRemediate_EveryCatalogCode TestRemediate_NoNumericBands TestRemediate_NotAllowlistedBodyConstant \
  TestDispatcherCaller_UsesRPCWire TestScopeRefusal_NamesRequiredScope \
  TestEvalSet/f1-missing-message-is-not-found \
  TestRemediate_CodeCollisionsBothTransports TestRemediate_MissingUnknownReasonIsInternal TestRPCWire_EveryRowCarriesReason
```

#### S2b — scope-filtered tools, scope introspection, allowlist pre-flight (F2, F3)

FR-116-11/12/14 and SR-116-09.

Acceptance:

```bash
set -euo pipefail
cd "$(git rev-parse --show-toplevel)"
test "$(git rev-parse HEAD)" = "${PR_HEAD_SHA:?set to the tested PR head SHA}"
export GOMAXPROCS=2 GOFLAGS=-p=2
go build ./...
go vet ./...
OUT=$(mktemp)
showfail() { echo "FAILED: $1; fail/build events from $OUT:" >&2; jq -rR '(fromjson? // {Action:"raw",Output:.}) | select(.Action=="fail" or .Action=="build-fail" or .Action=="raw" or .Action=="build-output" or (.Action=="output" and ((.Test==null and (.Output|test("^(PASS|ok\\s|-test\\.shuffle)")|not)) or (.Output|test("^\\s*--- FAIL|_test\\.go:[0-9]+:|panic:"))))) | if (.Action|test("output|raw")) then (.Output|rtrimstr("\n")) else "\(.Action) \(.Package // .ImportPath // "") \(.Test // "")" end' "$OUT" >&2 || true; }
trap 'showfail "$BASH_COMMAND"' ERR
go test -json -race -shuffle=on -count=1 -run '^(TestMCPScope_ToolListIsFunctionOfScope|TestMCPScope_NoAdminMutationTools|TestMCPScope_UnlistedToolIsUnknown|TestAuthWhoami_MatchesRequiredScope|TestAllowCheck_ReadScopeAllowed|TestAllowCheck_DoesNotEnumerate|TestAllowCheck_Audited)$' \
  ./cmd/wad/ ./internal/mcpbridge/ ./internal/adapters/primary/rest/ | tee "$OUT" >/dev/null
go test -json -count=1 -run '^(TestToolSnapshot|TestEvalSet)$/^(f2-preflight-before-send|f3-scope-shown-before-call)?$' ./internal/mcpbridge/evals/ | tee -a "$OUT" >/dev/null
python3 scripts/assert-tests-ran.py "$OUT" \
  TestMCPScope_ToolListIsFunctionOfScope TestMCPScope_NoAdminMutationTools TestMCPScope_UnlistedToolIsUnknown \
  TestAuthWhoami_MatchesRequiredScope TestAllowCheck_ReadScopeAllowed TestAllowCheck_DoesNotEnumerate \
  TestAllowCheck_Audited TestToolSnapshot \
  TestEvalSet/f2-preflight-before-send TestEvalSet/f3-scope-shown-before-call
```

#### S3 — media read tools

FR-116-15: `wa_list_media`, `wa_get_media`, the narrowing-filter rule, list handles,
size caps, wrapped captions, and audit `via:"mcp"`.

Acceptance:

```bash
set -euo pipefail
cd "$(git rev-parse --show-toplevel)"
test "$(git rev-parse HEAD)" = "${PR_HEAD_SHA:?set to the tested PR head SHA}"
export GOMAXPROCS=2 GOFLAGS=-p=2
go build ./...
go vet ./...
OUT=$(mktemp)
showfail() { echo "FAILED: $1; fail/build events from $OUT:" >&2; jq -rR '(fromjson? // {Action:"raw",Output:.}) | select(.Action=="fail" or .Action=="build-fail" or .Action=="raw" or .Action=="build-output" or (.Action=="output" and ((.Test==null and (.Output|test("^(PASS|ok\\s|-test\\.shuffle)")|not)) or (.Output|test("^\\s*--- FAIL|_test\\.go:[0-9]+:|panic:"))))) | if (.Action|test("output|raw")) then (.Output|rtrimstr("\n")) else "\(.Action) \(.Package // .ImportPath // "") \(.Test // "")" end' "$OUT" >&2 || true; }
trap 'showfail "$BASH_COMMAND"' ERR
go test -json -race -shuffle=on -count=1 -run '^(TestMCPMedia_RequiresNarrowingFilter|TestMCPMedia_HandleBindsTokenAndRow|TestMCPMedia_ExpiredHandleRejected|TestMCPMedia_SizeCap|TestMCPMedia_CaptionStaysWrapped|TestMCPMedia_Audited|TestMCPMedia_HandleEncodingUnambiguous|TestMCPMedia_CaptionMinimumLength|TestMCPMedia_TimeAndCaptionFields)$' \
  ./internal/mcpbridge/ ./cmd/wad/ | tee "$OUT" >/dev/null
go test -json -count=1 -run '^(TestToolSnapshot|TestEvalSet)$/^(media-list-requires-sender-or-caption)?$' ./internal/mcpbridge/evals/ | tee -a "$OUT" >/dev/null
python3 scripts/assert-tests-ran.py "$OUT" \
  TestMCPMedia_RequiresNarrowingFilter TestMCPMedia_HandleBindsTokenAndRow TestMCPMedia_ExpiredHandleRejected \
  TestMCPMedia_SizeCap TestMCPMedia_CaptionStaysWrapped TestMCPMedia_Audited TestToolSnapshot \
  TestEvalSet/media-list-requires-sender-or-caption \
  TestMCPMedia_HandleEncodingUnambiguous TestMCPMedia_CaptionMinimumLength TestMCPMedia_TimeAndCaptionFields
```

#### S4 — deadlines and owned jobs (F6)

FR-116-16: the `MethodDeadlines` table, job-capable `sync.force` / `media.download`,
`jobs.get` / `jobs.cancel`, owner binding, the admin override, and the MCP tools.
Time-sensitive tests use `testing/synctest` with in-memory fakes, and the job reaper gets
goleak coverage (R31). The cancellation-resistant fake ignores its context until a
controlled release after 30 fake seconds: every transport must already have returned
its job id at 10 s, and polling must later expose its one result. Inventory tests cover
Pair and every other detached handler, including cancellation propagation and retained
slots until a stubborn worker exits. Pair keeps human-only admin scope; no pairing MCP
tool is added.

Acceptance:

```bash
set -euo pipefail
cd "$(git rev-parse --show-toplevel)"
test "$(git rev-parse HEAD)" = "${PR_HEAD_SHA:?set to the tested PR head SHA}"
export GOMAXPROCS=2 GOFLAGS=-p=2
go build ./...
go vet ./...
OUT=$(mktemp)
showfail() { echo "FAILED: $1; fail/build events from $OUT:" >&2; jq -rR '(fromjson? // {Action:"raw",Output:.}) | select(.Action=="fail" or .Action=="build-fail" or .Action=="raw" or .Action=="build-output" or (.Action=="output" and ((.Test==null and (.Output|test("^(PASS|ok\\s|-test\\.shuffle)")|not)) or (.Output|test("^\\s*--- FAIL|_test\\.go:[0-9]+:|panic:"))))) | if (.Action|test("output|raw")) then (.Output|rtrimstr("\n")) else "\(.Action) \(.Package // .ImportPath // "") \(.Test // "")" end' "$OUT" >&2 || true; }
trap 'showfail "$BASH_COMMAND"' ERR
go test -json -race -shuffle=on -count=1 -run '^(TestMethodDeadlines_EveryMethodClassified|TestNoMethodBlocksOver10s|TestMethodDeadline_IgnoresCancellationReturnsJob|TestMethodDeadlines_DetachedInventoryAndCancellation|TestMethodDeadline_CancelRetainsWorkerSlot|TestSyncForce_SyncModeDeadline|TestMediaDownload_SyncModeDeadline|TestSyncJobs_Bounded|TestSyncJobs_CrossTokenDenied|TestSyncJobs_AdminOverrideAudited|TestSyncJobs_Cancel|TestSyncJobs_CancelledOnTokenRevoke|TestSyncJobs_ReaperNoLeak|TestWaitForReply_Clamped|TestMethodDeadline_InFlightSeparateFromJobCaps|TestMethodDeadline_PromotionFullRemainsBounded|TestSyncJobs_SocketPrincipal|TestMethodDeadline_NoSelfReexecution|TestSyncJobs_DirectStoreRevokeAndExpiry|TestSyncForce_JobCarriesGapReachability|TestCLI_PairPollsWithoutRestart|TestCLI_WaitHonoursOverallDeadline)$' \
  ./cmd/wad/ ./internal/app/ ./internal/mcpbridge/ ./internal/adapters/primary/rest/ ./cmd/wa/ | tee "$OUT" >/dev/null
go test -json -count=1 -run '^(TestToolSnapshot|TestEvalSet)$/^(f6-sync-returns-job-id)?$' ./internal/mcpbridge/evals/ | tee -a "$OUT" >/dev/null
python3 scripts/assert-tests-ran.py "$OUT" \
  TestMethodDeadlines_EveryMethodClassified TestNoMethodBlocksOver10s \
  TestMethodDeadline_IgnoresCancellationReturnsJob TestMethodDeadlines_DetachedInventoryAndCancellation \
  TestMethodDeadline_CancelRetainsWorkerSlot TestSyncForce_SyncModeDeadline \
  TestMediaDownload_SyncModeDeadline TestSyncJobs_Bounded TestSyncJobs_CrossTokenDenied \
  TestSyncJobs_AdminOverrideAudited TestSyncJobs_Cancel TestSyncJobs_CancelledOnTokenRevoke \
  TestSyncJobs_ReaperNoLeak TestWaitForReply_Clamped TestToolSnapshot TestEvalSet/f6-sync-returns-job-id \
  TestMethodDeadline_InFlightSeparateFromJobCaps TestMethodDeadline_PromotionFullRemainsBounded TestSyncJobs_SocketPrincipal TestMethodDeadline_NoSelfReexecution TestSyncJobs_DirectStoreRevokeAndExpiry TestSyncForce_JobCarriesGapReachability TestCLI_PairPollsWithoutRestart TestCLI_WaitHonoursOverallDeadline
```

#### S5 — admin listener, login, session security, status page

FR-116-01, FR-116-07, and SR-116-01..05, 07, 08: the separate loopback listener and its
bind validation, `wad admin-tunnel`, pre-login and session CSRF, Origin validation, the
cookie, the rate limit, the shared audit helper with login/logout records, the status
page, CSP, and escaping. CSP **and frame-ancestors land in S5a** with the first
listener/templates, not S5b; escaping and audit-failure protection do too. The only state-changing routes are the admin session's own
login and logout, and both ship CSRF and audit here.

Acceptance:

```bash
set -euo pipefail
cd "$(git rev-parse --show-toplevel)"
test "$(git rev-parse HEAD)" = "${PR_HEAD_SHA:?set to the tested PR head SHA}"
export GOMAXPROCS=2 GOFLAGS=-p=2
go build ./...
go vet ./...
OUT=$(mktemp)
showfail() { echo "FAILED: $1; fail/build events from $OUT:" >&2; jq -rR '(fromjson? // {Action:"raw",Output:.}) | select(.Action=="fail" or .Action=="build-fail" or .Action=="raw" or .Action=="build-output" or (.Action=="output" and ((.Test==null and (.Output|test("^(PASS|ok\\s|-test\\.shuffle)")|not)) or (.Output|test("^\\s*--- FAIL|_test\\.go:[0-9]+:|panic:"))))) | if (.Action|test("output|raw")) then (.Output|rtrimstr("\n")) else "\(.Action) \(.Package // .ImportPath // "") \(.Test // "")" end' "$OUT" >&2 || true; }
trap 'showfail "$BASH_COMMAND"' ERR
go test -json -race -shuffle=on -count=1 -run '^(TestAdminListener_DisabledByDefault|TestAdminListener_RefusesNonLoopbackBind|TestAdminListener_RefusesPortCollision|TestAdmin_NotMountedOnRESTMux|TestAdmin_PublicRequestWithForgedHostRejected|TestAdmin_NonLoopbackPeerRejected|TestAdmin_InsecureNonLoopbackOriginRejected|TestAdmin_SessionCookieEstablishedOverLoopbackHTTP|TestAdmin_SessionCookieEstablishedViaHTTPSOrigin|TestAdmin_SessionCookieAttributes|TestAdmin_ForwardedProtoIgnored|TestAdmin_RequiresAdminScope|TestAdmin_LoginCSRFRejected|TestAdmin_CSRFRejected|TestAdmin_LoginAudited|TestAdmin_LogoutAudited|TestAdmin_LoginRateLimited|TestAdmin_TokenNeverLogged|TestAdmin_CSPHeader|TestAdmin_EscapesUntrustedNames|TestAdmin_StatusMatchesReadyz|TestAdminTunnel_BridgesToLoopbackOnly|TestAdmin_DirectStoreRevokeAndExpiry|TestAdmin_RevocationSweepWithin30s|TestAdminTunnel_LocalForwarderLoopbackOnly|TestAdmin_AuditFailureFailsClosed)$' \
  ./internal/adapters/primary/admin/ ./internal/adapters/primary/rest/ ./cmd/wad/ | tee "$OUT" >/dev/null
python3 scripts/assert-tests-ran.py "$OUT" \
  TestAdminListener_DisabledByDefault TestAdminListener_RefusesNonLoopbackBind TestAdminListener_RefusesPortCollision \
  TestAdmin_NotMountedOnRESTMux TestAdmin_PublicRequestWithForgedHostRejected TestAdmin_NonLoopbackPeerRejected \
  TestAdmin_InsecureNonLoopbackOriginRejected TestAdmin_SessionCookieEstablishedOverLoopbackHTTP \
  TestAdmin_SessionCookieEstablishedViaHTTPSOrigin TestAdmin_SessionCookieAttributes TestAdmin_ForwardedProtoIgnored \
  TestAdmin_RequiresAdminScope TestAdmin_LoginCSRFRejected TestAdmin_CSRFRejected TestAdmin_LoginAudited \
  TestAdmin_LogoutAudited TestAdmin_LoginRateLimited TestAdmin_TokenNeverLogged TestAdmin_CSPHeader \
  TestAdmin_EscapesUntrustedNames TestAdmin_StatusMatchesReadyz TestAdminTunnel_BridgesToLoopbackOnly \
  TestAdmin_DirectStoreRevokeAndExpiry TestAdmin_RevocationSweepWithin30s TestAdminTunnel_LocalForwarderLoopbackOnly TestAdmin_AuditFailureFailsClosed
# assets: templates must exist (absent fails) and be free of external or inline sources (grep 1 = clean, 0 = hit, 2 = error)
D=internal/adapters/primary/admin/templates
ls "$D"/*.html >/dev/null
rc=0; grep -rnE 'https?://|cdn\.|<script|<style|style=|(^|[^[:alnum:]_])[dD][aA][tT][aA]:' "$D" || rc=$?
test "$rc" -eq 1
```

#### S6 — pairing and unlink (F4)

FR-116-02/03 and SR-116-06: the strict-CSP render mode for `internal/pairhtml`, the QR
image endpoint, the lifecycle state machine with every cancellation trigger, late-event
handling, and the typed-confirm unlink. Pair start, pair cancel and unlink each ship CSRF
and audit tests here. The tests use the whatsmeow fake client; there is no live pairing
in this slice.

Acceptance:

```bash
set -euo pipefail
cd "$(git rev-parse --show-toplevel)"
test "$(git rev-parse HEAD)" = "${PR_HEAD_SHA:?set to the tested PR head SHA}"
export GOMAXPROCS=2 GOFLAGS=-p=2
go build ./...
go vet ./...
OUT=$(mktemp)
showfail() { echo "FAILED: $1; fail/build events from $OUT:" >&2; jq -rR '(fromjson? // {Action:"raw",Output:.}) | select(.Action=="fail" or .Action=="build-fail" or .Action=="raw" or .Action=="build-output" or (.Action=="output" and ((.Test==null and (.Output|test("^(PASS|ok\\s|-test\\.shuffle)")|not)) or (.Output|test("^\\s*--- FAIL|_test\\.go:[0-9]+:|panic:"))))) | if (.Action|test("output|raw")) then (.Output|rtrimstr("\n")) else "\(.Action) \(.Package // .ImportPath // "") \(.Test // "")" end' "$OUT" >&2 || true; }
trap 'showfail "$BASH_COMMAND"' ERR
go test -json -race -shuffle=on -count=1 -run '^(TestAdminPair_StartRequiresCSRF|TestAdminPair_StartAudited|TestAdminPair_QRNotPersisted|TestAdminPair_SingleViewer|TestAdminPair_NonceSingleUse|TestAdminPair_ExplicitCancel|TestAdminPair_CancelledOnTimeout|TestAdminPair_CancelledOnSessionLogout|TestAdminPair_CancelledOnSessionExpiry|TestAdminPair_CancelledOnTokenRevoke|TestAdminPair_CancelDropsQRReferences|TestAdminPair_QRFetchAfterCancelIs404|TestAdminPair_LateCodeEventDropped|TestAdminPair_SuccessAfterCancelAudited|TestAdminPair_AlreadyPaired|TestAdminLogout_RequiresTypedConfirm|TestAdminLogout_CSRFAndAudit|TestRender_StrictCSPMode|TestAdminPair_MetaRefreshCadence|TestAdminPair_CancelledOnDaemonShutdown|TestAdminPair_CancelledOnTokenExpiry|TestAdminPair_DirectStoreRevokeDeniesEveryQRRequest|TestAdminPair_AuditFailureFailsClosed)$' \
  ./internal/adapters/primary/admin/ ./cmd/wad/ ./internal/adapters/secondary/whatsmeow/ ./internal/pairhtml/ | tee "$OUT" >/dev/null
python3 scripts/assert-tests-ran.py "$OUT" \
  TestAdminPair_StartRequiresCSRF TestAdminPair_StartAudited TestAdminPair_QRNotPersisted TestAdminPair_SingleViewer \
  TestAdminPair_NonceSingleUse TestAdminPair_ExplicitCancel TestAdminPair_CancelledOnTimeout \
  TestAdminPair_CancelledOnSessionLogout TestAdminPair_CancelledOnSessionExpiry TestAdminPair_CancelledOnTokenRevoke \
  TestAdminPair_CancelDropsQRReferences TestAdminPair_QRFetchAfterCancelIs404 TestAdminPair_LateCodeEventDropped \
  TestAdminPair_SuccessAfterCancelAudited TestAdminPair_AlreadyPaired TestAdminLogout_RequiresTypedConfirm \
  TestAdminLogout_CSRFAndAudit TestRender_StrictCSPMode \
  TestAdminPair_MetaRefreshCadence TestAdminPair_CancelledOnDaemonShutdown TestAdminPair_CancelledOnTokenExpiry TestAdminPair_DirectStoreRevokeDeniesEveryQRRequest TestAdminPair_AuditFailureFailsClosed
```

#### S7 — allowlist, tokens and audit tail

FR-116-04/05/06: allowlist add/remove per action, token issue (shown once) and revoke,
session drop on revoke, and the audit tail. Each mutation ships CSRF and audit tests.

Acceptance:

```bash
set -euo pipefail
cd "$(git rev-parse --show-toplevel)"
test "$(git rev-parse HEAD)" = "${PR_HEAD_SHA:?set to the tested PR head SHA}"
export GOMAXPROCS=2 GOFLAGS=-p=2
go build ./...
go vet ./...
OUT=$(mktemp)
showfail() { echo "FAILED: $1; fail/build events from $OUT:" >&2; jq -rR '(fromjson? // {Action:"raw",Output:.}) | select(.Action=="fail" or .Action=="build-fail" or .Action=="raw" or .Action=="build-output" or (.Action=="output" and ((.Test==null and (.Output|test("^(PASS|ok\\s|-test\\.shuffle)")|not)) or (.Output|test("^\\s*--- FAIL|_test\\.go:[0-9]+:|panic:"))))) | if (.Action|test("output|raw")) then (.Output|rtrimstr("\n")) else "\(.Action) \(.Package // .ImportPath // "") \(.Test // "")" end' "$OUT" >&2 || true; }
trap 'showfail "$BASH_COMMAND"' ERR
go test -json -race -shuffle=on -count=1 -run '^(TestAdminAllowlist_AddRemovePerAction|TestAdminAllowlist_CSRFRejected|TestAdminAllowlist_Audited|TestAdminAllowlist_MasksJIDs|TestAdminTokens_IssueShownOnceAndAudited|TestAdminTokens_RevokeAuditedAndDropsSessions|TestAdminTokens_CannotRevokeOwnSessionToken|TestAdminTokens_CSRFRejected|TestAdminAudit_TailVerifiesChain|TestAdminAudit_BrokenChainBanner|TestAdminTokens_DirectStoreRevokeAndExpiry|TestAdminAllowlist_AuditFailureFailsClosed|TestAdminTokens_AuditFailureFailsClosed)$' \
  ./internal/adapters/primary/admin/ ./cmd/wad/ | tee "$OUT" >/dev/null
python3 scripts/assert-tests-ran.py "$OUT" \
  TestAdminAllowlist_AddRemovePerAction TestAdminAllowlist_CSRFRejected TestAdminAllowlist_Audited \
  TestAdminAllowlist_MasksJIDs TestAdminTokens_IssueShownOnceAndAudited TestAdminTokens_RevokeAuditedAndDropsSessions \
  TestAdminTokens_CannotRevokeOwnSessionToken TestAdminTokens_CSRFRejected TestAdminAudit_TailVerifiesChain \
  TestAdminAudit_BrokenChainBanner \
  TestAdminTokens_DirectStoreRevokeAndExpiry TestAdminAllowlist_AuditFailureFailsClosed TestAdminTokens_AuditFailureFailsClosed
```

#### S8 — export and time fields (F7)

FR-116-17 on the RPC surface too (`export`, `history`, `messages.list`, `media.list`): add
`timestamp_unix_s` + `timestamp_at`, `has_caption`, and the wrapped inbound `caption`.
The existing `timestamp` and `channel` stay for compatibility, with a deprecation note in
`docs/manual.md`.

Acceptance:

```bash
set -euo pipefail
cd "$(git rev-parse --show-toplevel)"
test "$(git rev-parse HEAD)" = "${PR_HEAD_SHA:?set to the tested PR head SHA}"
export GOMAXPROCS=2 GOFLAGS=-p=2
go build ./...
go vet ./...
OUT=$(mktemp)
showfail() { echo "FAILED: $1; fail/build events from $OUT:" >&2; jq -rR '(fromjson? // {Action:"raw",Output:.}) | select(.Action=="fail" or .Action=="build-fail" or .Action=="raw" or .Action=="build-output" or (.Action=="output" and ((.Test==null and (.Output|test("^(PASS|ok\\s|-test\\.shuffle)")|not)) or (.Output|test("^\\s*--- FAIL|_test\\.go:[0-9]+:|panic:"))))) | if (.Action|test("output|raw")) then (.Output|rtrimstr("\n")) else "\(.Action) \(.Package // .ImportPath // "") \(.Test // "")" end' "$OUT" >&2 || true; }
trap 'showfail "$BASH_COMMAND"' ERR
go test -json -race -shuffle=on -count=1 -run '^(TestWireMessage_TimeFieldsCarryUnits|TestWireMessage_InboundCaptionWrapped|TestWireMessage_HasCaption|TestMediaList_TimeFieldsCarryUnits|TestExport_TimeFieldsCarryUnits)$' \
  ./cmd/wad/ ./cmd/wa/ | tee "$OUT" >/dev/null
go test -json -count=1 -run '^TestEvalSet$/^f7-time-fields-carry-units$' ./internal/mcpbridge/evals/ | tee -a "$OUT" >/dev/null
python3 scripts/assert-tests-ran.py "$OUT" \
  TestWireMessage_TimeFieldsCarryUnits TestWireMessage_InboundCaptionWrapped TestWireMessage_HasCaption \
  TestMediaList_TimeFieldsCarryUnits TestExport_TimeFieldsCarryUnits TestEvalSet/f7-time-fields-carry-units
```

### Deferred operator runbook (not slice acceptance)

These steps are for the operator after the relevant slice merges. They are not part of any
slice's acceptance block, and no worker runs them (§8).

- **After S1 (canary).** Deploy to `wa-burocracy` first, then `wa-personal`. Between the
  two, confirm `/readyz` 200 and that `last_persisted_at` advances. The PR body carries
  the rollback: revert one commit, and `/readyz` returns to session-only (incident-class,
  R32).
- **After S5 (enabling admin on one daemon).**
  - Set `WAD_ADMIN_ADDR=127.0.0.1:<port>` and choose one reach path from SR-116-02:
    `tailscale serve`, never `funnel`, for a host-native daemon; the
    `wad admin-tunnel` SSH forward for a container.
  - Before adding a ts.net name to `WAD_ADMIN_HTTPS_ORIGINS`, check that the front passes
    the original `Host` through unchanged.
  - Confirm that `/admin` on the REST listener answers `404`.
  - T17: localhost cookies are shared across ports. Prefer a dedicated HTTPS origin;
    keep the SSH/container forwarder inside the operator trust boundary and bind its
    local socket to 127.0.0.1 only. A loopback bind does not isolate cookies by port.

**Open question for `/speckit:clarify`, not decided here:** should `message.edit` of the
caller's own message inside the edit window drop from `ScopeAdmin` to `ScopeSend`, the way
`message.revoke` with `scope:"self"` already does (`scope.go:185-214`)? F3's root
annoyance is that a send token cannot correct its own send. Changing a scope bar is a
policy decision, so this spec makes the requirement visible (FR-116-12) and leaves the
bar unchanged.

**Open protocol question (F9/F10):** could a synthetic or future anchor reach a tail gap
without waiting for a live message? This behaviour is unverified and is not a design
option or acceptance requirement. Only real persisted messages may anchor these slices.

### Spec-only failure-class check

Run against the fetched PR document (`S` is its path), alongside the brief's checks.
This is document validation, not a claim that future slice tests already exist.

```bash
set -euo pipefail
test -s "$S"
for f in F1 F2 F3 F4 F5 F6 F7 F9 F10; do
  grep -q "^### $f\\b" "$S" || { echo "MISSING $f"; exit 1; }
done
test "$(grep -c '^Verified on main:' "$S")" -ge 9
```

## 8. Out of scope for every slice

- No deploy, no Dokku app change, no env change on a running daemon, no new service.
  Setting `WAD_ADMIN_ADDR` and configuring `tailscale serve` belong to the deferred
  runbook.
- No live pairing, no unlink, no logout against a real account; pairing is tested with the
  fake whatsmeow client only.
- No token issued or revoked on a live daemon, and no allowlist change on a live daemon.
- No public exposure: no CDN route, no DNS change, no tunnel, no `tailscale funnel`;
  the admin listener stays off by default (`WAD_ADMIN_ADDR` unset).
- No MCP tool that mutates allowlist, pairing, session or tokens (SR-116-09).
- No change to the FR-005a envelope contract; new caption fields wrap, never unwrap.
- No renumbering of existing wire codes; `reason` disambiguates where codes collide
  (`-32099`).

## Review log

Cross-family gate 07/11/2026 (GPT-6.1 Sol, REJECT with 9 blocking items).
Revision 1 (`5ab18e79`, CI green) resolved B1/B2/B3/B5/B6/B9; the re-review found
B4/B7/B8 partial, with no new blocking defects. Revision 2 (08/11/2026) addresses those
remaining findings and adds the sanitised F9/F10 addendum. Gate 3 on `0b26f9dd`
(Claude opus, cross-family) was APPROVE-WITH-FIXES with B10–B13. Revision 3
(08/11/2026) addresses those plus non-blocking items 1–5. The coordinator handles merge
after document acceptance and CI; no fourth full gate is required. This log records
spec changes, not implementation completion:

- **B1** public reachability via headers → SR-116-02: separate loopback-only listener
  (`WAD_ADMIN_ADDR`), bind validation, not mounted on the REST mux, TCP-peer check; T1
  tests include a direct request with a forged `Host`. The header-based gate is removed.
- **B2** security landing late → §7 preamble rule plus S5: pre-login CSRF (SR-116-03),
  Origin validation, session security and the audit helper land with login. S6 and S7
  ship CSRF and audit tests with each mutation.
- **B3** HTTPS unspecified → SR-116-04: two accepted origins (loopback, configured HTTPS
  front), scheme from config only, forwarding headers ignored, trusted-proxy boundary;
  cookie-establishment and insecure-origin tests in S5.
- **B4** revision 2 → FR-116-21/22 and S1: only a later qualifying message-table
  write/representative transaction clears the latch, never a probe upsert or duplicate.
  `TestStorageProbe_UpsertSucceedsMessageInsertFullStays503` proves the exact
  successful-upsert / failed-message-`SQLITE_FULL` counterexample stays 503 past 5 min.
- **B5** contradictory health invariants → FR-116-23: one truth table; `/readyz` 200 iff
  no 503-column reason holds; `soft_stale` and `storage_low` are explicit warnings; all
  2⁷ combinations tested (S1).
- **B6** incomplete pairing lifecycle → SR-116-06: state machine, six cancellation
  triggers, dropped QR references, generation id for late events, success-after-cancel handling; S6
  tests.
- **B7** revision 2 → FR-116-16 and S4: outer return deadline, reserved owned job id
  even for omitted/false async and cancellation-resistant handlers; Pair and all detached
  handlers use job cancellation hooks. `TestMethodDeadline_IgnoresCancellationReturnsJob`
  checks a pollable job at 10 s and one late result; ownership/override tests remain.
- **B8** acceptance can pass falsely → §7 harness: `set -euo pipefail`, `go test -json`
  plus `scripts/assert-tests-ran.py` asserting each contracted test passed, a negative
  self-check in S1, and grep exit codes separating absent, clean and error. S2 is split
  into S2a and S2b with separate blocks. Revision 2 puts `go build ./...` and
  `go vet ./...` on separate lines in **every** slice block, so build failure aborts.
  The spec-only class loop includes F9/F10.
- **B9** live canary inside slice acceptance → §7 "Deferred operator runbook (not slice
  acceptance)"; S1's block is local and automated only.

- **B10** revision 3 → FR-116-21/22/23, S1: startup/periodic representative recovery
  transaction, ≥64 KiB floor, storage-only latch and logic-error counter; idle restart,
  duplicate-does-not-clear and constraint-does-not-latch tests.
- **B11** revision 3 → FR-116-16, S4: separate in-flight/job pools, bounded promotion
  overflow, explicit socket principal, no daemon re-execution; poll rather than resend.
- **B12** revision 3 → FR-116-05/16, SR-116-04/06, S4–S7: every admin/QR/jobs request
  revalidates backing tokens in store; ≤30 s sweep; direct-store revoke and expiry tests.
- **B13** revision 3 → FR-116-10, S2a: every RPCWire row has `error.data.reason`; both
  bridges preserve/map only it, missing/unknown → internal; both collisions tested on
  both transports.

- **F9** revision 2 → §1 F9, FR-116-24, S1b: deferred per-chat backfill on storage
  recovery or startup with unpersisted receipts, using the first real live commit as
  anchor; fake-clock transition/restart, opt-in, cooldown and exactly-once tests.
- **F10** revision 2 → §1 F10, FR-116-25, S1b: `gap_reachable:false` with a reason
  for an unreachable tail gap, fake-clock false/real-anchor tests, no synthetic anchor;
  the unverified protocol idea appears only as an open question, not a required fix.

Non-blocking items taken: N1 `required_scope` dropped from the FR-116-10 example; N2
clocks, duplicates and out-of-order completion defined in FR-116-22; N3 list-bound media
handles in FR-116-15; N4 the rate limit keys on the TCP peer (SR-116-07); N5 splits
predeclared in §7.

Gate-3 non-blocking items taken in revision 3: (1) CSP/frame-ancestors in S5a;
(2) T17 localhost-cookie/tunnel residual risk and 127.0.0.1 forwarder bind;
(3) 5 s meta-refresh, drop QR string references, shutdown/token-expiry cancellation tests;
(4) length-prefixed handles, S3 time/caption contract and caption minimum, S4 gap result
and pair/wait caller updates; (5) daemon-wide pull budget, explicit older-history flag,
repo-top-level/head assertions, data-URI-only asset grep and fail-closed audit intent.
