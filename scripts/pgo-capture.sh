#!/usr/bin/env bash
# pgo-capture.sh — capture a Profile-Guided Optimization profile for `wad`.
#
# Runs every benchmark in the daemon hot-path packages, captures one
# CPU pprof per package, merges them into `cmd/wad/default.pgo`. The
# Go toolchain auto-detects `default.pgo` next to `main.go` and feeds
# it to the compiler when building `./cmd/wad/...` — no flag needed.
#
# Re-run quarterly (or whenever a new hot path is identified) to keep
# the profile representative.
#
# Usage:  bash scripts/pgo-capture.sh
#         make pgo-capture
#
# Verification:
#   go build -pgo=auto -x ./cmd/wad/ 2>&1 | grep default.pgo

set -euo pipefail

REPO_ROOT="$(cd "$(dirname "$0")/.." && pwd)"
cd "$REPO_ROOT"

PROFILE_DIR="$(mktemp -d)"
trap 'rm -rf "$PROFILE_DIR"' EXIT

PACKAGES=(
  ./cmd/wad/
  ./internal/app/
  ./internal/adapters/primary/socket/
  ./internal/adapters/secondary/sqlitehistory/
  ./internal/adapters/secondary/whatsmeow/
)

i=0
for pkg in "${PACKAGES[@]}"; do
  out="$PROFILE_DIR/p${i}.pprof"
  echo "[pgo-capture] benching $pkg → $(basename "$out")"
  if ! go test -run=NONE -bench=. -benchtime=2s -count=1 \
       -cpuprofile="$out" "$pkg"; then
    echo "[pgo-capture] WARN: $pkg failed; continuing"
    rm -f "$out"
    continue
  fi
  if [ ! -s "$out" ]; then
    echo "[pgo-capture] $pkg: no profile written (no benches matched), skipping"
    rm -f "$out"
    continue
  fi
  i=$((i+1))
done

shopt -s nullglob
profiles=("$PROFILE_DIR"/*.pprof)
shopt -u nullglob
if [ ${#profiles[@]} -eq 0 ]; then
  echo "[pgo-capture] ERROR: no profiles captured" >&2
  exit 1
fi

OUTPUT="cmd/wad/default.pgo"
echo "[pgo-capture] merging ${#profiles[@]} profiles → $OUTPUT"
go tool pprof -proto -output="$OUTPUT" "${profiles[@]}"

bytes="$(wc -c <"$OUTPUT" | tr -d ' \t')"
samples="$(go tool pprof -top -unit=samples "$OUTPUT" 2>/dev/null \
           | awk '/Showing/ {print; exit}' || true)"
echo "[pgo-capture] wrote $OUTPUT ($bytes bytes)"
[ -n "$samples" ] && echo "[pgo-capture] $samples"
echo "[pgo-capture] verify pickup: go build -pgo=auto -x ./cmd/wad/ 2>&1 | grep default.pgo"
