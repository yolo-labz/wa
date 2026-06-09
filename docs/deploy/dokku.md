# Dokku deploy runbook

Production deploy of `wad` on a self-hosted Dokku 0.34+ instance. One
WhatsApp account per Dokku app. Multi-host CLI access via SSH-forwarded
unix socket. Spec 109.

## What this gives you

- One long-running `wad` daemon per Dokku app.
- Persistent SQLite state surviving deploys, restarts, host reboots.
- HTTP `/healthz` + `/readyz` probes for Dokku healthchecks.
- SSH-forwarded multi-host CLI: any machine with an SSH key on the
  Dokku host can run `wa send`, `wa messages`, `wa allow add`, etc.

## What this does NOT give you (yet)

- HTTPS REST API for non-CLI clients (browser, mobile). See spec 110.
- Token-based auth — auth today is SSH keys. See spec 110.
- Multi-region / active-active. The single-instance invariant on
  `session.db` makes this structurally impossible without a re-pair.

## Prerequisites

- Dokku 0.34 or newer (`dokku version`).
- Docker 24 or newer (Dokku is a Docker wrapper).
- One DNS record pointing at the Dokku host. Optional but recommended:
  also point a CNAME for `wa-status` so Dokku's letsencrypt buildpack
  can probe the `/healthz` endpoint via HTTP-01.
- SSH access to the Dokku host as the `dokku` user OR as a user in
  the `dokku` group with `sudo dokku` permissions.

## Stage 1 — host setup (one-time)

```bash
# On your local machine
git clone https://github.com/yolo-labz/wa.git
cd wa
scp dokku/post-deploy.sh dokku.example.com:/tmp/wa-post-deploy.sh

# On the Dokku host
ssh dokku.example.com
sudo bash /tmp/wa-post-deploy.sh wa
```

`post-deploy.sh` is idempotent — safe to re-run after a partial setup.
It creates the app, mounts `/data`, chowns the storage directory to
the distroless `nonroot` UID 65532, sets formation to `quantity=1
max_parallel=1`, and binds public port 80 to the container's `:8080`.

## Stage 2 — image build & push

Two options. Pick whichever fits your CI:

### Option A — git push to Dokku (in-cluster build)

```bash
git remote add dokku dokku@dokku.example.com:wa
git push dokku main
```

Dokku reads `Dockerfile` from the repo root and builds inside the
cluster. First build pulls golang:1.26-alpine (~400 MB) once; further
builds are incremental thanks to BuildKit cache mounts.

The `git push` path passes **no** `--build-arg`, so the version, commit,
and date are derived inside the build from the in-context `.git`
(Dockerfile builder stage self-stamps when an arg is at its default).
`wa version` / `wad`'s `system.hello` therefore report the real
`vX.Y.Z-N-gSHA` even on in-cluster builds — not `dev`. Option B below is
still the **canonical** path because its image is provenance-attested and
byte-reproducible; Option A is the zero-CI fallback.

### Option B — pre-built image push (CI-style)

```bash
docker build \
    --build-arg SOURCE_DATE_EPOCH="$(git log -1 --format=%ct)" \
    --build-arg VERSION="$(git describe --tags --always)" \
    --build-arg COMMIT="$(git rev-parse HEAD)" \
    -t ghcr.io/yolo-labz/wa:$(git rev-parse --short HEAD) .

docker push ghcr.io/yolo-labz/wa:$(git rev-parse --short HEAD)

# On the Dokku host:
ssh dokku.example.com dokku registry:login ghcr.io <user> <token>
ssh dokku.example.com dokku git:from-image wa ghcr.io/yolo-labz/wa:$(git rev-parse --short HEAD)
```

The `.github/workflows/docker-image.yml` workflow performs the **build +
push** half of Option B automatically on every push to `main` (and on
`v*` tags), publishing `ghcr.io/yolo-labz/wa:sha-<short>`,
`:main`, and `:latest` with a build-provenance attestation. It does
**not** deploy — Dokku does not auto-pull. To roll a freshly-built image
onto an app, run the `git:from-image` step yourself on the host:

```bash
# <sha> = short SHA the CI image was tagged with (git rev-parse --short HEAD on main)
ssh dokku.example.com dokku git:from-image wa ghcr.io/yolo-labz/wa:sha-<sha>
```

This is **zero-downtime** (Dokku health-gates the new container on
`/healthz` before retiring the old one) and the `/data` storage mount —
hence `session.db` — is preserved, so no re-pair. Pin to the immutable
`sha-<short>` tag, not `:latest`, so a redeploy is reproducible and you
always know which commit is live.

If the GHCR package is private, the host needs pull credentials once:
`dokku registry:login ghcr.io <user> <read:packages-token>`.

## Stage 3 — first-time pairing

The container starts with no WhatsApp session. The `/readyz` probe
returns 503 until a session exists.

```bash
# Tail the logs in one terminal so you see the daemon come up
ssh dokku.example.com dokku logs wa -t

# In another terminal, exec into the container and run `wa pair`
ssh dokku.example.com dokku enter wa -- /usr/local/bin/wa pair
```

`wa pair` prints a QR code in your terminal. Scan it with WhatsApp on
the phone you want to pair. Within ~5 seconds the daemon transitions
to `connected = true` and `/readyz` flips to 200.

Re-pairing (after `events.LoggedOut`):

```bash
ssh dokku.example.com dokku enter wa -- /usr/local/bin/wa pair
```

The daemon does NOT auto-restart on `LoggedOut`. Liveness probes keep
returning 200 (the process is fine), only readiness flips to 503 so
Dokku drains traffic. This is intentional — auto-restarting would
lose the session.db every cycle and burn through WhatsApp's reconnect
budget. See `internal/adapters/secondary/whatsmeow/` and the spec 109
research dossier for the rationale.

## Reliability — soft-stale watchdog + backfill

`LoggedOut` is the loud failure. The quiet one is a **soft stall**:
whatsmeow's keepalive still answers PINGs and `/readyz` stays 200, but
WhatsApp silently stopped delivering inbound traffic — a "zombie link".
The daemon looks healthy while a real person's messages vanish. (This bit
`wa-burocracy` on 09/06/2026: inbound voice notes were lost during a
~22 min zombie stall while `wa health` reported `connected:true`.)

Three opt-in env vars arm the spec-110g watchdog against this. They are
**off by default** (a quiet chat must not trip them); set them on a
production daemon whose whole job is to stay paired:

| Env var | Effect |
|---|---|
| `WA_SOFT_STALE_THRESHOLD_SEC` | Seconds of no-inbound-while-connected before the link is judged stale. `0`/unset disables the watchdog; clamped to `[30, 3600]`. Detection + `state.softStale` event only. |
| `WA_SOFT_STALE_RECOVER` | `1`/`true`/`yes`/`on` → on a healthy→stale edge, force one `Disconnect`+`Connect` (no QR, same session) to break the zombie link. Cooldown-bounded (300 s). |
| `WA_SOFT_STALE_BACKFILL` | `1`/`true`/`yes`/`on` → after a successful recover reconnect, issue a global on-demand history pull so the messages WhatsApp delivered into the dead socket get recovered. Requires `WA_SOFT_STALE_RECOVER` (a backfill over a still-zombie link is pointless — the daemon logs a warning and ignores it otherwise). |

Recommended production config. A `config:set` restarts the container, so
treat it as an incident-class deploy: apply to a canary first and verify
`wa health` shows `staleState:healthy` + a fresh inbound timestamp before
trusting it.

```bash
dokku config:set wa \
    WA_SOFT_STALE_THRESHOLD_SEC=300 \
    WA_SOFT_STALE_RECOVER=1 \
    WA_SOFT_STALE_BACKFILL=1
```

`dokku/post-deploy.sh` sets these as defaults for fresh app provisioning;
an existing app needs the `config:set` above once.

## Stage 4 — multi-host CLI access via SSH-forward

Any host with SSH access to the Dokku machine can drive the daemon:

```bash
# On the client machine (laptop, AI agent host, etc.)
brew install yolo-labz/tap/wa  # gets the `wa` binary

# Set the destination once per shell session
export WA_REMOTE_HOST=dokku.example.com

# Use scripts/wa-remote (ships in the wa repo) instead of bare `wa`:
./scripts/wa-remote status
./scripts/wa-remote messages --limit 10 --json
./scripts/wa-remote send --to 5511999999999 --body "ping from $(hostname)"
```

`wa-remote` SSH-forwards `/var/lib/dokku/data/storage/wa/run/default.sock`
on the Dokku host to a local temp socket, then execs `wa --socket
<localsock> <args>`. Cleanup on exit removes the local socket and
the SSH forward process.

## Backups

`wad` writes session.db (Signal Double Ratchet) and messages.db
(history + FTS5). Both are SQLite WAL-mode databases.

**Safe pattern** — use SQLite's online backup API:

```bash
# Hourly cron on the Dokku host
ssh dokku.example.com sudo -u dokku-65532 \
    sqlite3 /var/lib/dokku/data/storage/wa/data/wa/default/session.db \
    ".backup '/var/lib/dokku/data/storage/wa-backup/session-$(date +%Y%m%d-%H).db'"
```

**Unsafe pattern** — copying the raw file with `cp`:

```bash
# DO NOT — copies the WAL out-of-band, can produce a corrupt copy
cp session.db /backup/session.db
```

The online backup API is concurrent-writer-safe in WAL mode (verified
upstream by SQLite developers). See [SQLite WAL docs](https://sqlite.org/wal.html)
and [howtocorrupt](https://sqlite.org/howtocorrupt.html).

For continuous off-host replication:
[Litestream](https://litestream.io) writes WAL pages to S3 in real time
and has been battle-tested with mautrix-whatsapp's database files.

## File retention

Three on-disk artifacts grow over time. The daemon now caps its own
migration backups; the rolling JSONL log is rotated by host logrotate.

**Migration backups** (`…/state/wa/<profile>/backups/messages.db.*.bak`)
are capped at 5 by the daemon: `OpenWithBackups` prunes the set
oldest-first on every startup, so no host action is needed and a
pre-existing pile is swept on the next launch. `wa doctor` reports a set
that exceeds the cap (which now only means the daemon hasn't restarted
since the pile formed).

**The daemon log** (`…/state/wa/<profile>/wad.log`) is appended by `wad`
through an `O_APPEND` fd it holds for its whole lifetime — there is no
SIGHUP-reopen and no in-process (lumberjack) rotation, so **`copytruncate`
is mandatory**: a rename/create would orphan that fd and the daemon would
keep writing to a dead inode. Drop this on the host as
`/etc/logrotate.d/wad` (the stock `/etc/cron.daily/logrotate` runs it):

```
/var/lib/dokku/data/storage/wa-*/state/wa/*/wad.log {
    weekly
    maxsize 20M
    rotate 4
    compress
    delaycompress
    missingok
    notifempty
    copytruncate
    su root root
}
```

`su root root` is required because the files are owned by the distroless
runtime uid 65532, which has no host passwd name (logrotate 3.21 rejects a
bare-numeric `su` user). Root can write the storage volume, and
`copytruncate` truncates in place so the live `wad.log` keeps its 65532
owner. Verified: both daemons keep writing and stay Connected across a
forced rotation, with no restart.

## Troubleshooting

**`/healthz` returns 200 but `/readyz` returns 503**

Daemon is alive but not paired. Run `dokku enter wa -- /usr/local/bin/wa pair`.

**`wa-remote status` hangs forever**

SSH key not in `~dokku/.ssh/authorized_keys`. Test plain SSH:

```bash
ssh dokku.example.com echo ok
```

**`session.db: database is locked`**

Two `wad` processes against the same volume. Should be impossible if
`formation.web.quantity=1` is set, but if it ever happens:

```bash
ssh dokku.example.com dokku ps:scale wa web=0
ssh dokku.example.com dokku ps:scale wa web=1
```

**`StreamReplaced`**

Another device paired with the same WhatsApp account. Session is
invalidated. Re-pair: `dokku enter wa -- /usr/local/bin/wa pair`. Read CLAUDE.md
§Daemon to understand the invariant.

**Permission denied writing to `/data`**

Storage directory not chowned to UID 65532. Re-run
`dokku/post-deploy.sh wa` on the host.

## Re-pair from a remote workstation

When `events.LoggedOut` fires, the daemon emits a `pairing.required`
event and stops accepting WhatsApp traffic. Operators previously had
to memorise an SSH + `dokku enter` chain to re-pair. Feature 110e
collapses that into a single CLI flag.

### Three invocations

```bash
# QR in your local terminal (half-block UTF-8; scan from WhatsApp).
wa pair --remote ProxMox.Dokku:wa-burocracy

# QR also opens in your default browser on the workstation.
wa pair --remote ProxMox.Dokku:wa-burocracy --browser

# Phone-code path (no QR rendering — enter the 8-char code in WhatsApp).
wa pair --remote ProxMox.Dokku:wa-burocracy --phone +5511999999999
```

`--remote <ssh-host>:<dokku-app>` value shape. The host can be a
`~/.ssh/config` alias, FQDN, Tailscale name, or `user@host` — anything
`ssh` resolves. The colon-separated dokku app name is what
`dokku ps:report` lists.

### Common errors

- **SSH key not loaded.** `ssh` prompts for password or errors with
  `Permission denied (publickey)`. Run `ssh-add ~/.ssh/id_ed25519` and
  retry.
- **Dokku app missing on host.** `dokku enter` returns `App '<x>' does
  not exist`. Confirm with `ssh dokku-host dokku apps:list`.
- **Daemon already paired.** Use `wa session logout-all` (preserves
  history) or `wa panic` (full wipe) inside the container first via
  `ssh -t dokku.example.com 'dokku enter wa-burocracy -- /usr/local/bin/wa session logout-all'`, then re-pair.

`--remote https://wa.example.com` (the REST URL form from spec 110c) is
**refused** with exit 64 — pair requires SSH access to the host, not
the REST endpoint. Use the `<host>:<app>` form.

## Related specs

- [Spec 105 — first-class LID JID](../../specs/105-lid-jid-support/spec.md)
- [Spec 106 — IdentityResolver port](../../specs/106-identity-resolver-port/spec.md)
- [Spec 107 — addressing-mode preservation](../../specs/107-addressing-mode-history/spec.md)
- [Spec 108 — JID server-kind expansion](../../specs/108-server-kind-expansion/spec.md)
- [Spec 109 — Dokku deploy + SSH-forward CLI](../../specs/109-dokku-deploy/spec.md)
- Spec 110 — REST primary adapter (deferred; this PR's spec lists rejected alternatives)
- [Spec 110e — `wa pair --remote` SSH-chain UX](../../specs/110e-wa-pair-remote/spec.md)
