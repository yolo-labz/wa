#!/usr/bin/env bash
# Real unpaired CLI capture; reproduce with nix develop -c ./scripts/record-demo.sh.
# Timing, random sandbox names and build identity may differ between runs.
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$repo_root"
for tool in vhs go ffmpeg ffprobe jq; do
  command -v "$tool" >/dev/null || {
    echo "$tool not found — run inside nix develop." >&2
    exit 1
  }
done
vhs validate docs/assets/wa-demo.tape

# ponytail: serialize one checkout's output set. Separate worktrees can record
# concurrently; an interrupted SIGKILL needs manual lock inspection, not stealing.
lock_dir="$repo_root/docs/assets/.wa-demo.lock"
mkdir "$lock_dir" 2>/dev/null || {
  echo "Recording lock exists: $lock_dir; inspect its owner before retrying." >&2
  exit 1
}
env_root=
wad_pid=
cleanup() {
  if [[ -n "$wad_pid" ]]; then
    kill "$wad_pid" 2>/dev/null || true
    wait "$wad_pid" 2>/dev/null || true
  fi
  [[ -z "$env_root" ]] || rm -rf -- "$env_root"
  rmdir "$lock_dir"
}
trap cleanup EXIT
trap 'exit 130' INT
trap 'exit 143' TERM

# Keep Unix socket paths short even when TMPDIR is a deep agent scratch path.
# mktemp creates a private 0700 directory; never touch /tmp/wa-demo-env.
env_root="$(mktemp -d /tmp/wa-demo.XXXXXX)"
bin_dir="$env_root/bin"
mkdir -p "$bin_dir" "$env_root"/{home,run,data,state,config,cache,tmp,docs/assets}
version="$(git describe --tags --always --dirty)"
echo "==> building wa + wad ($version) in $env_root"
CGO_ENABLED=0 go build -ldflags "-X main.version=$version" -o "$bin_dir/wa" ./cmd/wa
CGO_ENABLED=0 go build -ldflags "-X main.version=$version" -o "$bin_dir/wad" ./cmd/wad

# No inherited WA_*, shell startup files, telemetry endpoints or real HOME/XDG.
# Both the daemon and VHS (including its terminal child) use this exact boundary.
demo_env=(env -i "PATH=$bin_dir:$PATH" "HOME=$env_root/home"
  "XDG_RUNTIME_DIR=$env_root/run" "XDG_DATA_HOME=$env_root/data"
  "XDG_STATE_HOME=$env_root/state" "XDG_CONFIG_HOME=$env_root/config"
  "XDG_CACHE_HOME=$env_root/cache" "TMPDIR=$env_root/tmp"
  "TERM=xterm-256color" "LANG=C.UTF-8" "WA_DEMO_ENV=$env_root/env.sh")
printf "export PS1='\$ '\n" >"$env_root/env.sh"

echo "==> starting an unpaired daemon"
"${demo_env[@]}" "$bin_dir/wad" >"$env_root/wad.log" 2>&1 &
wad_pid=$!
for _ in $(seq 1 40); do
  [[ -S "$env_root/run/wa/default.sock" ]] && break
  kill -0 "$wad_pid" 2>/dev/null || break
  sleep 0.25
done
[[ -S "$env_root/run/wa/default.sock" ]] || {
  echo "daemon did not come up; log:" >&2
  tail -20 "$env_root/wad.log" >&2
  exit 1
}

# Text equivalent: actual output, not a hand-authored transcript. Commands match
# the tape; revoke is HELP ONLY. Fail before publishing if a command fails.
{
  for command in '--version' 'doctor' 'status' 'allow list' 'msg revoke --help'; do
    printf '\n$ wa %s\n' "$command"
    read -r -a args <<<"$command"
    "${demo_env[@]}" "$bin_dir/wa" "${args[@]}"
  done
} >"$env_root/docs/assets/wa-demo.txt"

echo "==> recording real CLI output"
(
  cd "$env_root"
  "${demo_env[@]}" vhs "$repo_root/docs/assets/wa-demo.tape"
)
ffmpeg -hide_banner -loglevel error -y -ss 10 -i "$env_root/docs/assets/wa-demo.mp4" \
  -frames:v 1 -threads 0 "$env_root/docs/assets/wa-demo.png"
bash "$repo_root/scripts/check-demo.sh" "$env_root/docs/assets"
# All formats are staged and checked first; the output lock excludes recorders.
for ext in gif mp4 webm png txt; do
  cp "$env_root/docs/assets/wa-demo.$ext" "$repo_root/docs/assets/wa-demo.$ext"
done
echo '==> wrote docs/assets/wa-demo.{gif,mp4,webm,png,txt}'
