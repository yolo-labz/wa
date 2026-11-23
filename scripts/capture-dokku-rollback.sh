#!/usr/bin/env bash
# capture-dokku-rollback.sh <app>...   run on the Dokku host (needs sudo docker)
# Saves the RUNNING image of each wa app outside the Docker cache and writes a manifest that binds
# app, image id, git sha, archive hash and size. Keeps the newest 3 captures per app.
# Reads only the image id and `wa version --json` (no container Env, no secrets).
set -euo pipefail
root=${RETENTION_DIR:-$HOME/rollback-retention}
for app in "$@"; do
  cid=$(sudo -n docker ps -q --filter "label=com.dokku.app-name=$app" | head -1); [ -n "$cid" ] || { echo "capture: $app not running" >&2; exit 1; }
  img=$(sudo -n docker inspect -f '{{.Image}}' "$cid"); sha=$(sudo -n docker exec "$cid" /usr/local/bin/wa version --json | sed -n 's/.*"commit":"\([0-9a-f]*\)".*/\1/p')
  d="$root/$app"; mkdir -p "$d"; out="$d/${sha:0:12}-$(date -u +%Y%m%dT%H%M%SZ).tar.gz"
  sudo -n docker save "$img" | gzip -6 > "$out.part" && mv "$out.part" "$out"
  h=$(sha256sum "$out" | cut -d' ' -f1); sz=$(stat -c %s "$out")
  printf '{"app":"%s","image":"%s","git_sha":"%s","archive":"%s","sha256":"%s","bytes":%s,"captured_at":"%s"}\n' "$app" "$img" "$sha" "$(basename "$out")" "$h" "$sz" "$(date -u +%FT%TZ)" > "$out.manifest.json"
  ln -sfn "$(basename "$out").manifest.json" "$d/latest.manifest.json"
  ls -1t "$d"/*.tar.gz 2>/dev/null | tail -n +4 | while read -r old; do rm -f "$old" "$old.manifest.json"; done
  echo "capture: $app $sha $(basename "$out") $sz bytes"
done
