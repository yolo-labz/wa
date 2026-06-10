#!/usr/bin/env bash
# wa installer — fetches the latest GoReleaser release, verifies its
# SHA-256 against the published checksums.txt, and installs `wa` + `wad`
# to ~/.local/bin (override with WA_INSTALL_DIR).
#
# Usage (inspect first — it's short):
#   curl -fsSL https://raw.githubusercontent.com/yolo-labz/wa/main/install.sh -o install.sh
#   less install.sh && bash install.sh
# Or the one-liner, if you trust the repo:
#   curl -fsSL https://raw.githubusercontent.com/yolo-labz/wa/main/install.sh | bash
#
# Prefer a package manager when you have one:
#   brew install yolo-labz/tap/wa        # macOS + Linuxbrew
#   nix profile install github:yolo-labz/wa
#
# bash 3.2 compatible (macOS default shell constraints).

set -euo pipefail

REPO="yolo-labz/wa"
INSTALL_DIR="${WA_INSTALL_DIR:-$HOME/.local/bin}"

say()  { printf '%s\n' "$*"; }
fail() { printf 'install.sh: %s\n' "$*" >&2; exit 1; }

command -v curl >/dev/null 2>&1 || fail "curl is required"
command -v tar  >/dev/null 2>&1 || fail "tar is required"

case "$(uname -s)" in
  Linux)  os="linux" ;;
  Darwin) os="darwin" ;;
  *) fail "unsupported OS $(uname -s) — use the release tarballs directly" ;;
esac
case "$(uname -m)" in
  x86_64|amd64)  arch="amd64" ;;
  aarch64|arm64) arch="arm64" ;;
  *) fail "unsupported arch $(uname -m)" ;;
esac
[ "$os" = "darwin" ] && [ "$arch" = "amd64" ] && \
  fail "darwin/amd64 is not published — use 'brew install yolo-labz/tap/wa'"

if command -v sha256sum >/dev/null 2>&1; then
  sha_tool="sha256sum"
elif command -v shasum >/dev/null 2>&1; then
  sha_tool="shasum -a 256"
else
  fail "need sha256sum or shasum to verify the download"
fi

asset="wa_${os}_${arch}.tar.gz"
base="https://github.com/${REPO}/releases/latest/download"

tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT

say "Downloading ${asset} (latest release)…"
curl -fsSL -o "${tmp}/${asset}" "${base}/${asset}" \
  || fail "download failed — check https://github.com/${REPO}/releases"
curl -fsSL -o "${tmp}/checksums.txt" "${base}/checksums.txt" \
  || fail "checksums.txt download failed"

say "Verifying SHA-256 against checksums.txt…"
expected="$(grep " ${asset}\$" "${tmp}/checksums.txt" | awk '{print $1}')"
[ -n "$expected" ] || fail "no checksum entry for ${asset}"
actual="$(cd "$tmp" && $sha_tool "$asset" | awk '{print $1}')"
[ "$expected" = "$actual" ] || fail "checksum mismatch: expected ${expected}, got ${actual}"
say "Checksum OK (${actual})"
say "Tip: releases also carry GitHub provenance — verify with:"
say "  gh attestation verify ${asset} --repo ${REPO}"

mkdir -p "$INSTALL_DIR"
tar -xzf "${tmp}/${asset}" -C "$tmp"
install -m 0755 "${tmp}/wa"  "$INSTALL_DIR/wa"
install -m 0755 "${tmp}/wad" "$INSTALL_DIR/wad"

say ""
say "Installed wa + wad to ${INSTALL_DIR}"
case ":$PATH:" in
  *":${INSTALL_DIR}:"*) ;;
  *) say "NOTE: ${INSTALL_DIR} is not on your PATH — add it to your shell profile." ;;
esac
say ""
say "Quickstart:"
say "  wad &                       # start the daemon"
say "  wa pair                     # scan the QR with your phone"
say "  wa allow add <your-number> --actions send"
say "  wa send --to <your-number> --body 'hello from wa'"
say "  wad install-service         # make it persistent"
