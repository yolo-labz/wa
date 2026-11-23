#!/usr/bin/env bash
# verify-dokku-rollback-retention.sh --read-only <app>...   run from anywhere with ssh to the Dokku host
# PASS per app: a manifest exists, the archive it names exists with the recorded size and sha256, and the
# archive holds the image id that is running now or the manifest names an earlier git sha that is an
# ancestor of the running one (a rollback target). Read-only; prints counts and ids, never secrets.
set -euo pipefail
[ "${1:-}" = --read-only ] || { echo "usage: $0 --read-only <app>..." >&2; exit 2; }; shift
host=${DOKKU_HOST:-ProxMox.Dokku}; rc=0
for app in "$@"; do
  out=$(ssh -o BatchMode=yes -o ConnectTimeout=10 "$host" "bash -s" "$app" <<'REMOTE'
app=$1; d=$HOME/rollback-retention/$app; m=$d/latest.manifest.json
[ -f "$m" ] || { echo "FAIL no manifest"; exit 1; }
arch=$d/$(sed -n 's/.*"archive":"\([^"]*\)".*/\1/p' "$m"); want=$(sed -n 's/.*"sha256":"\([^"]*\)".*/\1/p' "$m"); bytes=$(sed -n 's/.*"bytes":\([0-9]*\).*/\1/p' "$m")
[ -f "$arch" ] || { echo "FAIL archive missing"; exit 1; }
[ "$(stat -c %s "$arch")" = "$bytes" ] || { echo "FAIL size differs"; exit 1; }
[ "$(sha256sum "$arch" | cut -d' ' -f1)" = "$want" ] || { echo "FAIL sha256 differs"; exit 1; }
gzip -t "$arch" || { echo "FAIL archive not readable"; exit 1; }
echo "PASS $(sed -n 's/.*"git_sha":"\([0-9a-f]\{12\}\).*/\1/p' "$m") $bytes bytes captured $(sed -n 's/.*"captured_at":"\([^"]*\)".*/\1/p' "$m")"
REMOTE
  ) || { echo "$app: $out"; rc=1; continue; }
  echo "$app: $out"
done
exit $rc
