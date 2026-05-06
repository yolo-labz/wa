# syntax=docker/dockerfile:1.10
#
# Multi-stage Dockerfile for `wa` (CLI) + `wad` (daemon). Spec 109.
#
# Build args wire reproducible-build flags from CI / local invocations:
#   --build-arg SOURCE_DATE_EPOCH=$(git log -1 --format=%ct)
#   --build-arg VERSION=$(git describe --tags --always)
#   --build-arg COMMIT=$(git rev-parse HEAD)
#
# Output:
#   gcr.io/distroless/static-debian12:nonroot final image, ~12 MB.
#   Both binaries land in /usr/local/bin/ as root-owned but world-execute,
#   running under the distroless `nonroot` user (uid:gid 65532:65532).
#
# Build pipeline mirrors `.goreleaser.yaml` flags:
#   CGO_ENABLED=0 -trimpath -buildvcs=true -buildmode=pie
#   -ldflags="-s -w -X main.version=... -X main.commit=... -X main.date=..."
#
# Repo invariants (CLAUDE.md §"Decisions already locked in"):
#   - CGO is forbidden. modernc.org/sqlite, not mattn/go-sqlite3.
#   - Daemon is single-instance per WhatsApp account.
#   - Distroless `nonroot` UID 65532 — Dokku storage:mount must chown.

ARG GO_VERSION=1.26.1

############################################################
# builder — compiles both binaries with reproducible flags  #
############################################################
FROM golang:${GO_VERSION}-alpine3.20 AS builder

ARG SOURCE_DATE_EPOCH=0
ARG VERSION=dev
ARG COMMIT=unknown

ENV CGO_ENABLED=0 \
    GOFLAGS="-trimpath -buildvcs=true" \
    GOOS=linux

WORKDIR /src

# Cache the go module graph independently of source. Buildkit's mount
# cache survives across builds on a CI runner with persistent storage.
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    go mod download -x

COPY . .

RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    set -eux; \
    DATE_RFC3339="$(date -u -d "@${SOURCE_DATE_EPOCH:-0}" -Iseconds 2>/dev/null \
        || date -u -r "${SOURCE_DATE_EPOCH:-0}" "+%Y-%m-%dT%H:%M:%SZ" 2>/dev/null \
        || echo unknown)"; \
    LDFLAGS="-s -w -buildid= -X main.version=${VERSION} -X main.commit=${COMMIT} -X main.date=${DATE_RFC3339}"; \
    go build -buildmode=pie -ldflags "${LDFLAGS}" -o /out/wad ./cmd/wad; \
    go build -buildmode=pie -ldflags "${LDFLAGS}" -o /out/wa  ./cmd/wa; \
    /out/wad --version || true; \
    /out/wa  --version || true

############################################################
# runtime — distroless static, nonroot, single-stage runtime
############################################################
FROM gcr.io/distroless/static-debian12:nonroot AS runtime

ARG VERSION=dev
ARG COMMIT=unknown
ARG SOURCE_DATE_EPOCH=0

LABEL org.opencontainers.image.title="wa" \
      org.opencontainers.image.description="WhatsApp automation daemon + CLI" \
      org.opencontainers.image.source="https://github.com/yolo-labz/wa" \
      org.opencontainers.image.url="https://github.com/yolo-labz/wa" \
      org.opencontainers.image.licenses="Apache-2.0" \
      org.opencontainers.image.version="${VERSION}" \
      org.opencontainers.image.revision="${COMMIT}" \
      org.opencontainers.image.created="${SOURCE_DATE_EPOCH}"

# Distroless `nonroot` is uid:gid 65532:65532. Dokku storage:mount
# MUST be chowned to this UID before the first deploy or the daemon
# fails to write session.db.
USER 65532:65532
WORKDIR /data

# Copy binaries with the runtime UID as owner so distroless can exec
# them without further setup.
COPY --from=builder --chown=65532:65532 /out/wad /usr/local/bin/wad
COPY --from=builder --chown=65532:65532 /out/wa  /usr/local/bin/wa

# XDG paths point at /data subdirectories. The `adrg/xdg` library
# re-reads these env vars on every call (verified at
# cmd/wad/profile.go:110-229), so no code change is needed to redirect
# session.db, messages.db, allowlist.toml, audit.log, etc.
ENV HOME=/data \
    XDG_DATA_HOME=/data/data \
    XDG_CONFIG_HOME=/data/config \
    XDG_STATE_HOME=/data/state \
    XDG_CACHE_HOME=/data/cache \
    XDG_RUNTIME_DIR=/data/run \
    WA_PROFILE=default \
    WAD_HEALTH_HTTP_ADDR=:8080

EXPOSE 8080

# Foreground process (Dokku / Docker / k8s drives lifecycle). SIGTERM
# is forwarded directly to the binary because there is no shell wrapper
# — distroless has no shell.
ENTRYPOINT ["/usr/local/bin/wad"]
