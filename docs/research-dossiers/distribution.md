# Distribution dossier — feature 006

**Status**: research-only. Not active in the build until feature 006 (`distribution`) lifts these files into the repo root and CI.
**Origin**: produced by the deep-research swarm during `001-research-bootstrap`. Verbatim copy preserved here so feature 006 does not have to re-derive.
**Verified**: 2026-04-06 against the live GoReleaser, indygreg/apple-platform-rs, and Apple Developer documentation.

## Summary

Single-binary cross-compile for darwin-arm64, linux-amd64, linux-arm64 from a Linux GitHub Actions runner. `CGO_ENABLED=0` (forced by `modernc.org/sqlite`). Sign and notarize macOS builds via `rcodesign` from Linux. Publish to GitHub Releases, a Homebrew tap, and a Nix flake.

## `.goreleaser.yaml`

```yaml
version: 2
project_name: wa

before:
  hooks:
    - go mod tidy

builds:
  - id: wa
    main: ./cmd/wa
    binary: wa
    env: [CGO_ENABLED=0]
    goos: [linux, darwin]
    goarch: [amd64, arm64]
    ignore:
      - goos: darwin
        goarch: amd64
    flags: [-trimpath]
    ldflags:
      - -s -w
      - -X main.version={{.Version}}
      - -X main.commit={{.Commit}}
      - -X main.date={{.CommitDate}}
    mod_timestamp: '{{ .CommitTimestamp }}'

  - id: wad
    main: ./cmd/wad
    binary: wad
    env: [CGO_ENABLED=0]
    goos: [linux, darwin]
    goarch: [amd64, arm64]
    ignore:
      - goos: darwin
        goarch: amd64
    flags: [-trimpath]
    ldflags:
      - -s -w
      - -X main.version={{.Version}}
    mod_timestamp: '{{ .CommitTimestamp }}'

archives:
  - id: default
    ids: [wa, wad]
    name_template: "{{ .ProjectName }}_{{ .Version }}_{{ .Os }}_{{ .Arch }}"
    formats: [tar.gz]
    format_overrides:
      - goos: windows
        formats: [zip]

checksum:
  name_template: 'checksums.txt'
  algorithm: sha256

snapshot:
  version_template: "{{ incpatch .Version }}-next"

changelog:
  sort: asc
  use: github
  filters:
    exclude: ['^docs:', '^test:', '^chore:']

release:
  github:
    owner: yolo-labz
    name: wa
  draft: false
  prerelease: auto

brews:
  - name: wa
    repository:
      owner: yolo-labz
      name: homebrew-tap
      token: "{{ .Env.HOMEBREW_TAP_GITHUB_TOKEN }}"
    homepage: "https://github.com/yolo-labz/wa"
    description: "Personal WhatsApp automation CLI in Go"
    license: "Apache-2.0"
    install: |
      bin.install "wa"
      bin.install "wad"
    test: |
      system "#{bin}/wa", "--version"
```

## `flake.nix`

```nix
{
  description = "wa - WhatsApp automation CLI";

  inputs = {
    nixpkgs.url = "github:NixOS/nixpkgs/nixos-unstable";
    flake-utils.url = "github:numtide/flake-utils";
  };

  outputs = {
    nixpkgs,
    flake-utils,
    ...
  }:
    flake-utils.lib.eachDefaultSystem (system: let
      pkgs = nixpkgs.legacyPackages.${system};
    in {
      packages.default = pkgs.buildGoModule {
        pname = "wa";
        version = "0.1.0";
        src = ./.;
        vendorHash = pkgs.lib.fakeHash; # replace after first build
        subPackages = ["cmd/wa" "cmd/wad"];
        env.CGO_ENABLED = 0;
        ldflags = ["-s" "-w" "-X main.version=0.1.0"];
        meta = with pkgs.lib; {
          description = "Personal WhatsApp automation CLI";
          homepage = "https://github.com/yolo-labz/wa";
          license = licenses.asl20;
          mainProgram = "wa";
        };
      };

      devShells.default = pkgs.mkShell {
        packages = with pkgs; [
          go
          gopls
          golangci-lint
          goreleaser
          git-cliff
          lefthook
          sqlite
        ];
      };
    });
}
```

## `.github/workflows/release.yml`

```yaml
name: release

on:
  push:
    tags: ['v*']

permissions:
  contents: write

jobs:
  goreleaser:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
        with: {fetch-depth: 0}

      - uses: actions/setup-go@v5
        with: {go-version: 'stable', cache: true}

      - name: Install rcodesign
        run: |
          curl -sSL https://github.com/indygreg/apple-platform-rs/releases/download/apple-codesign%2F0.29.0/apple-codesign-0.29.0-x86_64-unknown-linux-musl.tar.gz \
            | tar -xz --strip-components=1 -C /usr/local/bin

      - name: Decode Apple credentials
        env:
          APPLE_DEVELOPER_ID_APPLICATION_CERT: ${{ secrets.APPLE_DEVELOPER_ID_APPLICATION_CERT }}
          APPLE_DEVELOPER_ID_APPLICATION_KEY: ${{ secrets.APPLE_DEVELOPER_ID_APPLICATION_KEY }}
          APPLE_API_KEY: ${{ secrets.APPLE_API_KEY }}
        run: |
          echo "$APPLE_DEVELOPER_ID_APPLICATION_CERT" | base64 -d > /tmp/cert.pem
          echo "$APPLE_DEVELOPER_ID_APPLICATION_KEY" | base64 -d > /tmp/key.pem
          echo "$APPLE_API_KEY" | base64 -d > /tmp/api.p8

      - uses: goreleaser/goreleaser-action@v6
        with:
          version: latest
          args: release --clean
        env:
          GITHUB_TOKEN: ${{ secrets.GITHUB_TOKEN }}
          HOMEBREW_TAP_GITHUB_TOKEN: ${{ secrets.HOMEBREW_TAP_GITHUB_TOKEN }}
          APPLE_API_KEY_PATH: /tmp/api.p8
          APPLE_API_KEY_ID: ${{ secrets.APPLE_API_KEY_ID }}
          APPLE_API_ISSUER: ${{ secrets.APPLE_API_ISSUER }}
          RCODESIGN_PEM_CERT: /tmp/cert.pem
          RCODESIGN_PEM_KEY: /tmp/key.pem
```

Add a `builds.hooks.post: rcodesign sign --pem-source /tmp/key.pem --pem-source /tmp/cert.pem {{ .Path }}` on the darwin builds, plus a post-archive `rcodesign notary-submit --api-key-path /tmp/api.p8 --staple` step for end-to-end notarization in the same job.

## One-time runbook

1. **Apple Developer Program** — enroll at <https://developer.apple.com> ($99/yr).
2. **Developer ID Application certificate** — Certificates → "+" → Developer ID Application. Export `.p12`. Split: `openssl pkcs12 -in cert.p12 -out cert.pem -clcerts -nokeys` and `openssl pkcs12 -in cert.p12 -out key.pem -nocerts -nodes`.
3. **App Store Connect API key** — <https://appstoreconnect.apple.com> → Users and Access → Integrations → App Store Connect API → "+" with role **Developer**. Download the `.p8` immediately (Apple never shows it again). Note Key ID and Issuer ID.
4. **GitHub Actions secrets** on `yolo-labz/wa`:
   - `APPLE_DEVELOPER_ID_APPLICATION_CERT` — `base64 < cert.pem`
   - `APPLE_DEVELOPER_ID_APPLICATION_KEY` — `base64 < key.pem`
   - `APPLE_API_KEY` — `base64 < AuthKey_XXX.p8`
   - `APPLE_API_KEY_ID` — 10-char key ID
   - `APPLE_API_ISSUER` — issuer UUID
5. **Homebrew tap repo** — create empty `yolo-labz/homebrew-tap` (must be named `homebrew-…`). Generate a fine-grained PAT with `contents: write` on it. Store as repo secret `HOMEBREW_TAP_GITHUB_TOKEN`.
6. **Nix flake first build** — run `nix build` once locally; copy the `vendorHash` Nix prints into `flake.nix`.
7. **Cut a release**: `git tag v0.1.0 && git push --tags`. GoReleaser builds, signs, notarizes, publishes the GitHub Release, updates the tap, attaches checksums, and rolls the changelog.

## Reference projects

- [`cli/cli`](https://github.com/cli/cli/blob/trunk/.goreleaser.yml)
- [`superfly/flyctl`](https://github.com/superfly/flyctl/blob/master/.goreleaser.yml)
- [`charmbracelet/gum`](https://github.com/charmbracelet/gum/blob/main/.goreleaser.yml)
- [`goreleaser/goreleaser`](https://github.com/goreleaser/goreleaser/blob/main/.goreleaser.yaml)

## Sources

- <https://goreleaser.com/customization/>
- <https://goreleaser.com/customization/builds/go/>
- <https://goreleaser.com/customization/homebrew/>
- <https://github.com/indygreg/apple-platform-rs>
- <https://nixos.wiki/wiki/Go>
- <https://nixos.org/manual/nixpkgs/stable/#sec-language-go>
- <https://developer.apple.com/documentation/security/notarizing-macos-software-before-distribution>
