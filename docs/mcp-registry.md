# MCP Registry + Claude Desktop distribution

Two artifacts make `wa mcp serve` one-click installable:

1. **`.mcpb` bundles** — [MCP Bundle](https://github.com/modelcontextprotocol/mcpb)
   zip files (manifest.json + the `wa` binary). Claude Desktop installs
   them by drag-and-drop: Settings → Extensions → drop the file. The
   manifest exposes two install-time options — *Send Mode*
   (`draft`/`direct`/`deny`, default `draft`) and *Read-Only Mode* —
   and runs `wa mcp serve` against the local `wad` daemon.
2. **`server.json`** — the manifest the
   [official MCP Registry](https://registry.modelcontextprotocol.io)
   indexes (`io.github.yolo-labz/wa`), pointing at the `.mcpb` release
   assets pinned by sha256. Aggregators (Smithery, Glama, PulseMCP)
   crawl the registry, so one listing fans out.

## Build (local or CI)

```bash
goreleaser release --snapshot --clean   # or a real release
scripts/build-mcpb.sh 2.2.0             # → dist/mcpb/wa_2.2.0_<os>_<arch>.mcpb
scripts/gen-mcp-server-json.sh 2.2.0 > server.json
```

One bundle per goreleaser target (`darwin_arm64`, `linux_amd64`,
`linux_arm64`) because the mcpb format keys `platform_overrides` by OS
only — there is no arch dimension inside one bundle.

Bundles require only `jq` + `zip`. Sanity-check a manifest with the
reference CLI: `npx @anthropic-ai/mcpb validate dist/mcpb-stage/manifest.json`.

## Publish to the registry

Publishing happens from the release workflow (needs `id-token: write`
for GitHub OIDC — the `io.github.yolo-labz/*` namespace is proven by
org membership):

```bash
mcp-publisher login github-oidc
mcp-publisher publish server.json
```

Manual fallback from a workstation: `mcp-publisher login github`
(device flow) then publish the same file.

## Daemon prerequisite

The bundle ships the CLI only. Users still install + pair the daemon
(`wad` + `wa pair`) from the [release archives](https://github.com/yolo-labz/wa/releases)
— the manifest's long description and the install error
(`wad daemon unreachable … hint: start it with `wad``) both say so.
