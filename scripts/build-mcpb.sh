#!/usr/bin/env bash
# Build .mcpb bundles (MCP Bundle — zip + manifest.json, the one-click
# Claude Desktop install format) from goreleaser-built `wa` binaries.
#
# An .mcpb has no arch dimension (platform_overrides is OS-keyed only),
# so we emit one bundle per goreleaser target:
#
#   wa_<version>_darwin_arm64.mcpb
#   wa_<version>_linux_amd64.mcpb
#   wa_<version>_linux_arm64.mcpb
#
# Usage: scripts/build-mcpb.sh <version> [dist-dir] [out-dir]
#   version   release version WITHOUT the v prefix (e.g. 2.2.0)
#   dist-dir  goreleaser dist directory (default: dist)
#   out-dir   where bundles land (default: <dist-dir>/mcpb)
#
# Requires: jq, zip. Spec: https://github.com/modelcontextprotocol/mcpb
set -euo pipefail

version="${1:?usage: build-mcpb.sh <version> [dist-dir] [out-dir]}"
dist="${2:-dist}"
out="${3:-${dist}/mcpb}"
tmpl="$(dirname "$0")/../mcpb/manifest.tmpl.json"

[ -f "$tmpl" ] || { echo "manifest template not found: $tmpl" >&2; exit 1; }
mkdir -p "$out"

built=0
for target in darwin_arm64 linux_amd64 linux_arm64; do
  os="${target%%_*}"
  arch="${target#*_}"

  # goreleaser layout: dist/<build-id>_<os>_<arch>[_<variant>]/wa
  bin=""
  for d in "$dist"/wa_"${os}"_"${arch}"*/; do
    [ -x "${d}wa" ] && bin="${d}wa" && break
  done
  if [ -z "$bin" ]; then
    echo "skip ${target}: no wa binary under ${dist}/" >&2
    continue
  fi

  stage="$(mktemp -d)"
  trap 'rm -rf "$stage"' EXIT
  mkdir -p "$stage/server"
  cp "$bin" "$stage/server/wa"
  chmod 0755 "$stage/server/wa"

  jq --arg version "$version" --arg platform "$os" \
    '.version = $version | .compatibility.platforms = [$platform]' \
    "$tmpl" >"$stage/manifest.json"

  bundle="$out/wa_${version}_${os}_${arch}.mcpb"
  rm -f "$bundle"
  # -X strips OS-specific extra fields; with the fixed mtimes goreleaser
  # binaries already carry, the zip is byte-reproducible.
  (cd "$stage" && zip -q -X -r - manifest.json server) >"$bundle"
  rm -rf "$stage"
  trap - EXIT

  echo "built $bundle"
  built=$((built + 1))
done

[ "$built" -gt 0 ] || { echo "no bundles built — wrong dist dir?" >&2; exit 1; }
