#!/usr/bin/env bash
# Generate the MCP Registry server.json for a release.
#
# Emits a manifest conforming to the 2025-12-11 server.schema.json with
# one `mcpb` package entry per bundle built by build-mcpb.sh, each
# pinned by sha256 to its GitHub release asset URL. Publish with the
# official CLI:
#
#   mcp-publisher login github-oidc   # inside GitHub Actions
#   mcp-publisher publish server.json
#
# Usage: scripts/gen-mcp-server-json.sh <version> [mcpb-dir] > server.json
#   version   release version WITHOUT the v prefix (e.g. 2.2.0)
#   mcpb-dir  directory holding the .mcpb bundles (default: dist/mcpb)
set -euo pipefail

version="${1:?usage: gen-mcp-server-json.sh <version> [mcpb-dir]}"
mcpbdir="${2:-dist/mcpb}"
repo_url="https://github.com/yolo-labz/wa"

packages="[]"
for target in darwin_arm64 linux_amd64 linux_arm64; do
  bundle="$mcpbdir/wa_${version}_${target}.mcpb"
  [ -f "$bundle" ] || { echo "skip ${target}: $bundle missing" >&2; continue; }
  sha="$(sha256sum "$bundle" | cut -d' ' -f1)"
  packages="$(jq --arg url "${repo_url}/releases/download/v${version}/$(basename "$bundle")" \
    --arg sha "$sha" \
    '. + [{registryType: "mcpb", identifier: $url, fileSha256: $sha, transport: {type: "stdio"}}]' \
    <<<"$packages")"
done

[ "$(jq length <<<"$packages")" -gt 0 ] || { echo "no .mcpb bundles in $mcpbdir" >&2; exit 1; }

jq -n --arg version "$version" --arg repo "$repo_url" --argjson packages "$packages" '{
  "$schema": "https://static.modelcontextprotocol.io/schemas/2025-12-11/server.schema.json",
  name: "io.github.yolo-labz/wa",
  title: "wa — safe WhatsApp automation",
  description: "Safety-first WhatsApp tools: draft-gated sends, enforced rate limits, append-only audit.",
  version: $version,
  websiteUrl: $repo,
  repository: { url: $repo, source: "github" },
  packages: $packages
}'
