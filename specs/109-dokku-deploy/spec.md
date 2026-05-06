# Feature 109 — Dokku deploy + multi-host SSH-forward CLI

**Branch**: `109-dokku-deploy`
**Status**: implemented
**Source**: operator request to centralise the WhatsApp daemon on a
shared host so any workstation, mobile, or AI agent can hook into one
running session — a single paired account, many clients.

## Problem

Today `wad` runs on a single workstation. Multi-host access means
re-pairing each machine, and each pairing is destructive: the previous
session is invalidated by `events.StreamReplaced`. Operator wants ONE
running daemon, MANY clients — the architecture already anticipates
this (REST primary adapter is stubbed at
`internal/adapters/primary/rest/`), but no deploy artefacts exist.

## Decision

Two-stage rollout. Stage 1 (this PR): deploy `wad` on Dokku as a
plain unix-socket daemon backed by persistent storage; multi-host
access via SSH-forwarded socket. Stage 2 (spec 110, future PR): REST
primary adapter with bearer-token auth and SSE for events.

Stage 1 deliverables:

- **Multi-stage `Dockerfile`** — golang:1.26 builder + `gcr.io/distroless/static-debian12:nonroot` runtime. CGO_ENABLED=0 verbatim from existing `.goreleaser.yaml` build matrix. Single image carries both `wa` and `wad` so `dokku enter wa <subcommand>` works for every CLI op (pair, allow, status, audit tail).
- **`.dockerignore`** — excludes `.git`, `dist/`, `result*`, `specs/`, `.specify/`, `node_modules/`, `coverage*`, `.envrc`, `*.test`, `*.out`. Keeps build context small (~10 MB instead of ~200 MB).
- **`dokku/app.json`** — declares persistent storage mount (`/data`), zero-downtime-disabled (`scale: 1`), pre-deploy `wad migrate` hook, env baseline (`XDG_*` pointing at `/data`).
- **`dokku/CHECKS`** — TCP healthcheck against `/healthz` on `WAD_HEALTH_HTTP_ADDR`. Non-canonical-Dokku-version-safe pattern; works on Dokku 0.34+.
- **`cmd/wad/health_http.go`** — tiny HTTP health endpoint, opt-in via `WAD_HEALTH_HTTP_ADDR` env. Reports `200 ok` when daemon's `subscribe` channel is open and the whatsmeow client reports `IsConnected()`. Reports `503` while pairing or while in re-connect backoff. Closed during graceful shutdown so Dokku stops routing traffic. ~80 LOC.
- **`scripts/wa-remote`** — Bash helper that SSH-forwards the host-side `/var/lib/wa/run/<profile>.sock` to a local path, then `exec wa --socket <localsock> "$@"`. Auth = SSH keys. Multi-host = anyone with key in `~dokku/.ssh/authorized_keys` on the Dokku host.
- **`docs/deploy/dokku.md`** — runbook: app create, plugin install, storage mount, build push, first-pair via `dokku enter wa -- /usr/local/bin/wa pair`, post-deploy verification.
- **`.github/workflows/docker-image.yml`** — build + push to `ghcr.io/yolo-labz/wa:<sha>` on every `main` commit and `:vN.N.N` on release tags. Uses GitHub-attest-build-provenance per `~/NixOS/meta/yolo-labz-release-engineering-research.md` §Supply chain.

Code change is intentionally minimal: ONE new file (`health_http.go`, opt-in via env), no changes to the existing daemon main, no changes to the protocol, no new auth surface, no new ports. The XDG-based path resolution `adrg/xdg` already re-reads env vars on every call, so pointing `XDG_DATA_HOME` etc. at `/data` is a configuration-only change. Verified at `cmd/wad/profile.go:110-229`.

## Alternatives rejected

Per Constitution rule 20.

### A. Run `wad` on bare Dokku host (no container)

`dokku ps:start` against a Procfile + buildpack would skip the Docker
image entirely. **Rejected** because:

1. Dokku's Go buildpack does not honour `CGO_ENABLED=0` reliably across
   buildpack versions, and the resulting binary depends on host glibc.
   Container is hermetic.
2. Reproducible builds (`SOURCE_DATE_EPOCH` + `-trimpath`) are part of
   the supply-chain commitment per release-engineering plan; buildpacks
   don't pin enough.
3. `gcr.io/distroless/static-debian12:nonroot` runs as UID 65532 with
   no shell, no package manager, no setuid binaries — strictly smaller
   attack surface than a `dokku/herokuish` Ubuntu base.

### B. Implement REST adapter in this PR

Combine Stage 1 + Stage 2. **Rejected** because:

1. REST adapter is the larger surface — auth (PASETO/JWT/opaque),
   TLS posture, SSE event streaming, token issuance/rotation/revocation
   admin commands. Constitution rule 6 caps `tasks.md` at ~25 items per
   feature. Stage 1 alone is ~12 items; combining doubles the PR.
2. SSH-forwarded unix socket already solves "any host hooks into one
   instance" with zero new auth surface (auth = SSH keys, encryption =
   SSH transport). Most operators already use SSH; this rides the
   existing trust path.
3. REST adapter wants its own adversarial review (Codex pass on
   token-rotation race conditions, header-trust assumptions behind a
   reverse proxy, SSE reconnect semantics). Stage 1 has zero new
   security primitives, so the review surface is just "Dockerfile +
   shell helper".

### C. Run multiple `wad` replicas behind a load balancer

The classic 12-factor scale-out pattern. **Rejected** because:

1. `wad` owns a Signal Double Ratchet store; two replicas writing to
   the same `session.db` corrupt the ratchet. WhatsApp's server side
   would emit `events.StreamReplaced` continuously. CLAUDE.md §Daemon
   already hard-codes this with `flock(LOCK_EX|LOCK_NB)` on the SQLite
   store.
2. WhatsApp pairs ONE device per QR scan. Multiple replicas means
   multiple devices means a fan-out problem at the protocol layer the
   daemon does not solve.
3. Dokku's `scale=1` declaration enforces the invariant.

## Functional requirements

- **FR-001** — `Dockerfile` produces a `wa` image where `/usr/local/bin/wad` and `/usr/local/bin/wa` are present, executable, and run as the `nonroot` (UID 65532) user.
- **FR-002** — `docker run -e XDG_DATA_HOME=/data ... wa wad` starts the daemon in foreground mode, accepts SIGTERM for graceful shutdown within 5 seconds, and writes session state to `/data/<profile>/session.db`.
- **FR-003** — `dokku/app.json` declares `/data` as a persistent volume; the storage mount survives `dokku ps:rebuild` and `dokku deploy`.
- **FR-004** — When `WAD_HEALTH_HTTP_ADDR` is set, `wad` listens on that address and responds `200 OK` to `GET /healthz` while the whatsmeow client reports `IsConnected() == true`, otherwise `503 Service Unavailable`. Shuts down cleanly on SIGTERM.
- **FR-005** — `scripts/wa-remote` forwards a unix socket via SSH and execs `wa --socket <local> "$@"`. Cleanup on exit removes the local socket.
- **FR-006** — `docs/deploy/dokku.md` carries a complete runbook including first-time pairing via `dokku enter wa -- /usr/local/bin/wa pair` and re-pairing after `events.LoggedOut`.
- **FR-007** — `.github/workflows/docker-image.yml` builds and pushes the image to `ghcr.io/yolo-labz/wa` on every push to `main` and on `v*` tags, with build-provenance attestation.

## Out of scope

- **REST/HTTPS primary adapter** — spec 110.
- **`wa --remote https://wa.example.com --token $TOKEN` CLI mode** — spec 111, depends on 110.
- **Token issuance/rotation/revocation** — spec 110.
- **TLS termination for non-HTTP unix-socket protocol** — Dokku's letsencrypt-buildpack only terminates HTTP/HTTPS; SSH-forward path side-steps this entirely.
- **Multi-region active-active** — single-instance invariant precludes; out of scope by design.

## Tests

- **`internal/observability/health_http_test.go`** — pins FR-004: `200` on connected, `503` on disconnected, `Shutdown()` returns within 1s.
- **`cmd/wad/health_http_test.go`** — wires the HTTP endpoint into a real `*Adapter` test double and asserts the listener address handshake.
- **Manual:** `docker build .` + `docker run --rm wa wad --version` produces the GoReleaser-shaped version string.
- **Manual:** `bash scripts/wa-remote --dry-run` prints the SSH command it would execute, exits 0.

## References

- `cmd/wad/profile.go:110-229` — XDG path resolution.
- `cmd/wad/main.go:628` — existing SIGTERM `signal.NotifyContext`.
- `.goreleaser.yaml` builds — `CGO_ENABLED=0`, `-trimpath`, `-buildvcs=true`, `-X main.version=$VERSION`.
- `flake.nix` — Nix-based reproducible build, parallel artefact path; this Dockerfile mirrors its build flags.
- `.specify/memory/constitution.md:138` — rule 20 rejected-alternatives requirement.
- `~/NixOS/meta/yolo-labz-release-engineering-plan.md` — supply-chain attestation pattern.
- Spec 110 (deferred) — REST adapter design; this PR's Dockerfile leaves the door open by making the daemon entrypoint accept additional CLI flags without baking deploy-time choices into the image.

## Future work (linked specs)

- **Spec 110** `internal/adapters/primary/rest/` — bearer-token JSON-RPC over HTTPS + SSE for events.
- **Spec 111** `wa --remote <url> --token <tok>` CLI mode.
- **Spec 112** Token admin (`wad token issue|revoke|list`), key rotation cadence.
