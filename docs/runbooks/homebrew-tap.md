# Runbook — Homebrew tap setup

This document explains how `brew install yolo-labz/tap/wa` is wired up and what a maintainer has to do ONCE to enable automatic publication of new formulas on every `v*` tag.

## Status

| Part | State |
|---|---|
| `yolo-labz/homebrew-tap` repo exists | ✅ public at <https://github.com/yolo-labz/homebrew-tap> |
| `Formula/wa.rb` bootstrap (v0.3.2) | ✅ hand-seeded from the v0.3.2 release tarballs |
| `brew install yolo-labz/tap/wa` works today | ✅ yes — the bootstrap formula is live |
| Automatic publication on future releases | ⚠️ needs `HOMEBREW_TAP_GITHUB_TOKEN` secret configured on `yolo-labz/wa` |

Until the token is set, every release still produces tarballs + checksums on GitHub Releases (that path is unblocked and works), but `brew upgrade yolo-labz/tap/wa` will stay pinned at v0.3.2 — new versions must be landed via a manual formula bump PR against the tap repo.

## One-time setup for automatic publication

1. **Create a GitHub Personal Access Token (PAT)** with write access to the tap repo.
   - Go to <https://github.com/settings/personal-access-tokens/new>
   - Name it `yolo-labz-homebrew-tap-publisher` or similar
   - Expiration: 1 year (or whatever your rotation policy is)
   - Resource owner: `yolo-labz`
   - Repository access: **Only select repositories** → `yolo-labz/homebrew-tap`
   - Repository permissions: **Contents: Read and write** (to commit formulas), **Metadata: Read**
   - No account permissions needed
   - Click **Generate token** and copy the `github_pat_...` value — it is shown only once.

   (A classic PAT with `public_repo` scope also works, but fine-grained is preferred.)

2. **Add the token as a secret on `yolo-labz/wa`**:

   ```bash
   gh secret set HOMEBREW_TAP_GITHUB_TOKEN --repo yolo-labz/wa
   # Paste the PAT value when prompted
   ```

   Or via the UI: `yolo-labz/wa` → Settings → Secrets and variables → Actions → New repository secret → name `HOMEBREW_TAP_GITHUB_TOKEN`.

3. **Verify** by re-running an existing release workflow (or cutting a new patch release). The goreleaser log should show:

   ```
     • homebrew tap formula
       • writing                                        formula=dist/homebrew/wa.rb
       • pushing                                        repo=yolo-labz/homebrew-tap
   ```

   — instead of the current "skipping (no token)" message.

## How it works (briefly)

- `.goreleaser.yaml` has a `brews:` block with:
  - `repository.owner = yolo-labz`
  - `repository.name  = homebrew-tap`
  - `repository.token = "{{ .Env.HOMEBREW_TAP_GITHUB_TOKEN }}"`
  - `skip_upload: '{{ if eq .Env.HOMEBREW_TAP_GITHUB_TOKEN "" }}true{{ else }}false{{ end }}'`

- When `goreleaser release` runs with the token set, it:
  1. Renders `dist/homebrew/wa.rb` from the release tarball URLs + SHA256s
  2. Clones `yolo-labz/homebrew-tap` using the PAT
  3. Commits `Formula/wa.rb` with a message like `Brew formula update for wa version 0.4.0`
  4. Pushes to the tap's `main` branch

- When the token is unset (current state), goreleaser's `skip_upload` template evaluates to `true` and the brew publication step is a no-op. The rest of the release (tarballs, checksums, CHANGELOG) runs unchanged.

## Manual formula bump (until automation is enabled)

If you need to ship a new version before the PAT is wired up, bump the formula by hand:

```bash
# Get checksums from the new release
curl -sL https://github.com/yolo-labz/wa/releases/download/vX.Y.Z/checksums.txt

# Edit the formula
gh repo clone yolo-labz/homebrew-tap /tmp/homebrew-tap
cd /tmp/homebrew-tap
$EDITOR Formula/wa.rb
#   - bump `version "X.Y.Z"`
#   - update all 3 url lines to the new tag
#   - update all 3 sha256 lines from the checksums.txt above

git -c user.email=you@example.com -c user.name="Your Name" commit -am "bump wa to vX.Y.Z"
git push origin main
```

Verify by running `brew update && brew upgrade yolo-labz/tap/wa` on a test machine.

## Validating the bootstrap formula locally

```bash
brew tap yolo-labz/tap
brew install yolo-labz/tap/wa
wa version
#   wa version 0.3.2   (for releases built via goreleaser ldflags)
wad --help | head -5
```

The bootstrap formula uses plain `bin.install "wa"` and `bin.install "wad"` with a `test { system "#{bin}/wa", "version" }` audit block. `brew audit --strict yolo-labz/tap/wa` should pass.

## Troubleshooting

### `Error: Cask 'wa' is unavailable: No Cask with this name exists.`

You're querying a Cask instead of a Formula. Use `brew install yolo-labz/tap/wa` (no `--cask`).

### `Error: wa: sha256 mismatch`

The formula's `sha256` doesn't match the release tarball — likely because the tarball was replaced by a second goreleaser run. Regenerate the formula from the current release checksums and push.

### `Error: invalid tap name 'yolo-labz/homebrew-tap'`

Tap names drop the `homebrew-` prefix. Use `yolo-labz/tap` (Homebrew rewrites it to `yolo-labz/homebrew-tap` internally).

### Automated publication fails with `fatal: Authentication failed`

The `HOMEBREW_TAP_GITHUB_TOKEN` secret is either missing, expired, or lacks `Contents: Read and write` on the tap repo. Re-issue the PAT and re-set the secret.

## Links

- Tap repo: <https://github.com/yolo-labz/homebrew-tap>
- Upstream repo: <https://github.com/yolo-labz/wa>
- GoReleaser brews docs: <https://goreleaser.com/customization/homebrew/>
- Homebrew tap authoring guide: <https://docs.brew.sh/How-to-Create-and-Maintain-a-Tap>
