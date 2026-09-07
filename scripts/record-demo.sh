#!/usr/bin/env bash
# Regenerate docs/assets/wa-demo.gif from docs/assets/wa-demo.tape.
#
#   ./scripts/record-demo.sh
#
# One command, clean checkout, no arguments. Timing jitter between runs is
# expected; the CONTENT must not change, because every frame is produced by
# running the real binaries rather than by editing a recording.
#
# The predecessor of this script did not exist: docs/assets/wa-demo.cast was
# a hand-authored file that drifted until it demonstrated a `wa daemon status`
# command the CLI never had. An unreproducible artifact cannot be reviewed,
# so this one is reproducible by construction.
#
# Isolation: the recorded daemon runs under a throwaway XDG root in a temp
# dir. It is never paired and never reaches WhatsApp, so no JID, phone
# number, or session data can appear in a frame. The one WARN `doctor` shows
# is that unpaired state — real output, not a staged one.
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$repo_root"

command -v vhs >/dev/null || {
  echo "vhs not found — run inside \`nix develop\` (it is in the devShell)." >&2
  exit 1
}

# A FIXED path, not mktemp: the sandbox directory is printed verbatim by
# `wa doctor` (socket + lockfile rows), so a random name would make every
# recording differ in content and defeat "regenerates identically".
env_root=/tmp/wa-demo-env
bin_dir="$env_root/bin"
rm -rf "$env_root"
mkdir -p "$bin_dir" "$env_root"/{run,data,state,config}

# Stamp the real version rather than the "dev" default, so the first frame
# does not contradict the release the README describes.
version="$(git describe --tags --abbrev=0 2>/dev/null || echo dev)"

cleanup() {
  if [[ -n "${wad_pid:-}" ]] && kill -0 "$wad_pid" 2>/dev/null; then
    kill "$wad_pid" 2>/dev/null || true
    wait "$wad_pid" 2>/dev/null || true
  fi
  rm -rf "$env_root"
}
trap cleanup EXIT

echo "==> building wa + wad ($version)"
go build -ldflags "-X main.version=$version" -o "$bin_dir/wa" ./cmd/wa
go build -ldflags "-X main.version=$version" -o "$bin_dir/wad" ./cmd/wad

# The tape sources this to enter the sandbox in one hidden line.
cat >"$env_root/env.sh" <<EOF
export XDG_RUNTIME_DIR=$env_root/run
export XDG_DATA_HOME=$env_root/data
export XDG_STATE_HOME=$env_root/state
export XDG_CONFIG_HOME=$env_root/config
export PATH=$bin_dir:\$PATH
export PS1='\$ '
EOF

echo "==> starting an unpaired daemon in $env_root"
(
  export XDG_RUNTIME_DIR="$env_root/run" XDG_DATA_HOME="$env_root/data"
  export XDG_STATE_HOME="$env_root/state" XDG_CONFIG_HOME="$env_root/config"
  exec "$bin_dir/wad"
) >"$env_root/wad.log" 2>&1 &
wad_pid=$!

for _ in $(seq 1 40); do
  [[ -S "$env_root/run/wa/default.sock" ]] && break
  sleep 0.25
done
[[ -S "$env_root/run/wa/default.sock" ]] || {
  echo "daemon did not come up; log:" >&2
  tail -20 "$env_root/wad.log" >&2
  exit 1
}

echo "==> recording"
vhs docs/assets/wa-demo.tape

bytes=$(stat -c%s docs/assets/wa-demo.gif 2>/dev/null || stat -f%z docs/assets/wa-demo.gif)
echo "==> docs/assets/wa-demo.gif — $((bytes / 1024)) KiB"
# A README GIF is hot-path bytes on every page view of a public repo.
if (( bytes > 2 * 1024 * 1024 )); then
  echo "WARNING: over the 2 MB budget — trim sleeps or drop a frame." >&2
fi
