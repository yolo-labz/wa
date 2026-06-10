#!/usr/bin/env bash
# Measures wad's resident set size after a clean unpaired boot — the
# "what does one always-on session cost" number. Reproducible: builds
# from the working tree, boots into a throwaway XDG root, samples RSS
# after 5s of settling, tears down.
set -euo pipefail
root="$(mktemp -d)"
trap 'kill "${pid:-0}" 2>/dev/null || true; rm -rf "$root"' EXIT
go build -o "$root/wad" ./cmd/wad
XDG_RUNTIME_DIR="$root/run" XDG_DATA_HOME="$root/data" \
XDG_CONFIG_HOME="$root/config" XDG_STATE_HOME="$root/state" \
  "$root/wad" >"$root/wad.log" 2>&1 &
pid=$!
sleep 5
rss_kb="$(awk '/VmRSS/{print $2}' "/proc/$pid/status" 2>/dev/null || ps -o rss= -p "$pid")"
echo "wad idle RSS: $((rss_kb / 1024)) MiB (${rss_kb} KiB)"
