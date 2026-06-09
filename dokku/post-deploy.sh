#!/usr/bin/env bash
# dokku/post-deploy.sh — one-time host setup for the `wa` Dokku app.
# Run this on the Dokku HOST (not inside the container) once before the
# first deploy. Spec 109 §"Stage 1 — Dokku deploy".
#
# Idempotent — safe to re-run after a failed first try. Will refuse to
# clobber an existing storage directory whose UID is wrong (script will
# print the offending UID and exit non-zero so you can investigate).
#
# Usage:
#   sudo bash dokku/post-deploy.sh [APP_NAME]
#
# Defaults APP_NAME to "wa".

set -euo pipefail

APP="${1:-wa}"
DOKKU_DATA_HOST_DIR="${DOKKU_DATA_HOST_DIR:-/var/lib/dokku/data/storage/${APP}}"
DISTROLESS_NONROOT_UID=65532
DISTROLESS_NONROOT_GID=65532

if [[ "$EUID" -ne 0 ]]; then
    echo "post-deploy: must run as root (sudo)" >&2
    exit 1
fi

# 1. Create persistent storage with the right ownership BEFORE the
#    storage:mount runs. Dokku's persistent-storage helpers do NOT chown
#    automatically for distroless UIDs (only for herokuish/heroku/paketo).
echo "post-deploy: provisioning ${DOKKU_DATA_HOST_DIR} (uid:gid ${DISTROLESS_NONROOT_UID}:${DISTROLESS_NONROOT_GID})"
mkdir -p "${DOKKU_DATA_HOST_DIR}"

if [[ -d "${DOKKU_DATA_HOST_DIR}" ]]; then
    current_uid=$(stat -c '%u' "${DOKKU_DATA_HOST_DIR}")
    current_gid=$(stat -c '%g' "${DOKKU_DATA_HOST_DIR}")
    if [[ "${current_uid}" != "${DISTROLESS_NONROOT_UID}" ]]; then
        echo "post-deploy: chown ${DOKKU_DATA_HOST_DIR}: ${current_uid}:${current_gid} -> ${DISTROLESS_NONROOT_UID}:${DISTROLESS_NONROOT_GID}"
        chown -R "${DISTROLESS_NONROOT_UID}:${DISTROLESS_NONROOT_GID}" "${DOKKU_DATA_HOST_DIR}"
    fi
fi
chmod 0700 "${DOKKU_DATA_HOST_DIR}"

# 2. Create the app if it does not exist. Idempotent.
if ! dokku apps:list 2>/dev/null | grep -qE "^${APP}$"; then
    echo "post-deploy: creating Dokku app ${APP}"
    dokku apps:create "${APP}"
fi

# 3. Mount the volume.
mount_already=$(dokku storage:list "${APP}" 2>/dev/null | grep -c ":${DOKKU_DATA_HOST_DIR}:/data" || true)
if [[ "${mount_already}" -eq 0 ]]; then
    echo "post-deploy: mounting ${DOKKU_DATA_HOST_DIR}:/data on ${APP}"
    dokku storage:mount "${APP}" "${DOKKU_DATA_HOST_DIR}:/data"
fi

# 4. Tune deploy timing — single-instance, ~10s graceful shutdown.
dokku config:set --no-restart "${APP}" \
    DOKKU_WAIT_TO_RETIRE=10 \
    DOKKU_CHECKS_ATTEMPTS=30 \
    DOKKU_DOCKER_STOP_TIMEOUT=30

# 4b. Arm the spec-110g soft-stale watchdog: detect a "zombie link"
#     (keepalive answers PINGs but WhatsApp stopped delivering inbound),
#     force one reconnect on the healthy->stale edge, then backfill the
#     messages missed during the stall. Off by default in the binary; a
#     production daemon whose job is staying paired wants them on.
#     THRESHOLD=900 (15min) catches a real stall — the 09/06/2026 incident
#     was ~22min — without tripping on normal quiet. wa-burocracy's inbound
#     cadence is ~5-6min, so a 300s threshold flapped (stale → reconnect
#     every ~6min, ~10/hr); 900s clears the normal gaps. Tune per app.
#     --no-restart so this host-setup pass doesn't bounce a live session —
#     the values take effect on the next deploy. Existing apps that skip a
#     redeploy need `dokku config:set <app> WA_SOFT_STALE_*` by hand.
#     See docs/deploy/dokku.md §Reliability.
dokku config:set --no-restart "${APP}" \
    WA_SOFT_STALE_THRESHOLD_SEC=900 \
    WA_SOFT_STALE_RECOVER=1 \
    WA_SOFT_STALE_BACKFILL=1

# 5. Pin formation to one replica. spec 109 §"Alternatives rejected (C)"
#    explains why scaling out is forbidden.
dokku ps:scale "${APP}" web=1

# 6. Bind public port 80 → container 8080 (health endpoint also handles
#    HTTP forwarding for future REST adapter, spec 110).
dokku ports:set "${APP}" http:80:8080

cat <<EOF

post-deploy: done.

Next steps:

  1. Push the image:
        git push dokku main
     OR
        docker build -t ${APP}/web:latest \\
            --build-arg SOURCE_DATE_EPOCH=\$(git log -1 --format=%ct) \\
            --build-arg VERSION=\$(git describe --tags --always) \\
            --build-arg COMMIT=\$(git rev-parse HEAD) .
        dokku tags:deploy ${APP} latest

  2. First-time pair (interactive, runs inside the container):
        dokku enter ${APP} -- /usr/local/bin/wa pair

  3. Tail the daemon log:
        dokku logs ${APP} -t

  4. From another host, hook in via SSH-forwarded socket:
        export WA_REMOTE_HOST=<dokku-host>
        scripts/wa-remote status

EOF
