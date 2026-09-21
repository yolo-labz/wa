#!/usr/bin/env bash
# Metadata and byte-budget gate; pass a staged asset directory or use the repo.
set -euo pipefail
assets="${1:-$(cd "$(dirname "${BASH_SOURCE[0]}")/../docs/assets" && pwd)}"
for ext in gif mp4 webm png; do
  file="$assets/wa-demo.$ext"
  budget=$((5 * 1024 * 1024))
  [[ "$ext" != gif ]] || budget=$((2 * 1024 * 1024))
  [[ "$ext" != png ]] || budget=$((1024 * 1024))
  bytes=$(wc -c <"$file")
  (( bytes > 0 && bytes <= budget )) || {
    echo "$file: $bytes bytes exceeds $budget budget (or empty)" >&2
    exit 1
  }
  metadata=$(ffprobe -v error -show_streams -show_format -of json "$file")
  jq -e --arg ext "$ext" '
    [.streams[] | select(.codec_type == "video")] as $video |
    ($video | length) == 1 and
    $video[0].width == 1100 and $video[0].height == 640 and
    ([.streams[] | select(.codec_type == "audio")] | length) == 0 and
    (if $ext == "png" then $video[0].codec_name == "png"
     else (.format.duration | tonumber) >= 10 and
          (.format.duration | tonumber) <= 60 end)
  ' <<<"$metadata" >/dev/null
  printf '%s: %s bytes, 1100x640, no audio, duration=%ss\n' \
    "$ext" "$bytes" "$(jq -r '.format.duration // "static"' <<<"$metadata")"
done
# These are captured CLI results, not a replacement for watching the frames.
grep -q 'device not paired' "$assets/wa-demo.txt"
grep -q '\$ wa allow list' "$assets/wa-demo.txt"
grep -q 'Neither is reversible' "$assets/wa-demo.txt"
echo 'PASS: media metadata, budgets and text equivalent'
