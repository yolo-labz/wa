#!/usr/bin/env bash
# install-dev-tools.sh — one-shot dev-tooling bootstrap for wa.
#
# Installs every tool the project's lefthook hooks + Makefile targets +
# `make sonar-local` reference. Idempotent — re-running on a fully
# tooled host is a no-op.
#
# Supports macOS (brew) and Linux (brew if available, otherwise direct
# binary download for the no-brew lane). Read the comments below for the
# exact source of each tool; bump versions in lockstep with CI pins.
#
# Usage:
#   ./scripts/install-dev-tools.sh           # install everything
#   ./scripts/install-dev-tools.sh check     # report what is / is not installed
#   ./scripts/install-dev-tools.sh --force   # re-install even if present

set -eu

MODE="${1:-install}"
case "$MODE" in
  install|check|--force)
    ;;
  *)
    echo "usage: $0 [install|check|--force]" >&2
    exit 64
    ;;
esac

OS="$(uname -s)"
ARCH="$(uname -m)"
case "$ARCH" in
  x86_64|amd64) ARCH=amd64 ;;
  arm64|aarch64) ARCH=arm64 ;;
  *) echo "unsupported arch: $ARCH" >&2; exit 1 ;;
esac

bindir="${HOME}/.local/bin"
mkdir -p "$bindir"

have() { command -v "$1" >/dev/null 2>&1; }

report() {
  local tool="$1"
  if have "$tool"; then
    printf '  [OK]   %-16s -> %s\n' "$tool" "$(command -v "$tool")"
  else
    printf '  [MISS] %-16s\n' "$tool"
  fi
}

if [ "$MODE" = "check" ]; then
  echo "Dev-tool inventory for wa:"
  for t in go golangci-lint gitleaks lefthook actionlint zizmor sonar-scanner shellcheck shfmt jq curl docker syft cosign goreleaser; do
    report "$t"
  done
  exit 0
fi

# ---- Homebrew lane ----------------------------------------------------------
# On macOS + Linuxbrew hosts, prefer brew so versions track the project's
# stable channel + future automated bumps.
if have brew; then
  echo "Using Homebrew (detected at $(command -v brew))"
  brew_pkgs="go golangci-lint gitleaks lefthook actionlint shellcheck shfmt jq sonar-scanner"
  for pkg in $brew_pkgs; do
    if [ "$MODE" = "--force" ] || ! brew list --formula "$pkg" >/dev/null 2>&1; then
      echo "  brew install $pkg"
      brew install "$pkg" || echo "    (install of $pkg failed — continuing)"
    else
      echo "  $pkg already installed"
    fi
  done
  # zizmor: no homebrew formula yet (April 2026). Use pipx or direct binary.
  if ! have zizmor || [ "$MODE" = "--force" ]; then
    if have pipx; then
      pipx install zizmor || echo "    (pipx install zizmor failed)"
    else
      echo "  zizmor: install pipx (brew install pipx) then \`pipx install zizmor\`"
    fi
  fi
  exit 0
fi

# ---- No-brew Linux lane ------------------------------------------------------
# Direct binary downloads, sha-pinned to match what the CI workflows install.
echo "No Homebrew detected — using direct binary download fallback"
echo "  bindir: $bindir"
echo "  Add \"$bindir\" to your PATH if not already present."

ACTIONLINT_VERSION="1.7.12"
ACTIONLINT_AMD64_SHA256="8aca8db96f1b94770f1b0d72b6dddcb1ebb8123cb3712530b08cc387b349a3d8"
ACTIONLINT_ARM64_SHA256="325e971b6ba9bfa504672e29be93c24981eeb1c07576d730e9f7c8805afff0c6"

ZIZMOR_VERSION="1.24.1"
ZIZMOR_AMD64_SHA256="a8000f3c683319a523d3b20df0e75457ba591f049cfcbfa98966631b56733c03"
ZIZMOR_ARM64_SHA256="d66e37ef8a375fb07939c630ebf9709a6e0f20242bdc3faf672a7ed97e0b768d"

GITLEAKS_VERSION="8.21.4"
LEFTHOOK_VERSION="1.7.22"

verify_and_extract() {
  local tarball="$1" expected="$2" extract_args="$3"
  local actual
  actual=$(sha256sum "$tarball" | awk '{print $1}')
  if [ "$actual" != "$expected" ]; then
    echo "ERROR: sha256 mismatch on $tarball — expected $expected got $actual" >&2
    return 1
  fi
  # shellcheck disable=SC2086
  tar xzf "$tarball" -C "$bindir" $extract_args
}

if ! have actionlint || [ "$MODE" = "--force" ]; then
  case "$ARCH" in
    amd64) sha="$ACTIONLINT_AMD64_SHA256" ;;
    arm64) sha="$ACTIONLINT_ARM64_SHA256" ;;
  esac
  tb="actionlint_${ACTIONLINT_VERSION}_linux_${ARCH}.tar.gz"
  echo "  -> actionlint v${ACTIONLINT_VERSION}"
  curl -sSfL -o "/tmp/${tb}" \
    "https://github.com/rhysd/actionlint/releases/download/v${ACTIONLINT_VERSION}/${tb}"
  verify_and_extract "/tmp/${tb}" "$sha" "actionlint"
  rm "/tmp/${tb}"
fi

if ! have zizmor || [ "$MODE" = "--force" ]; then
  case "$ARCH" in
    amd64) triple="x86_64-unknown-linux-gnu"; sha="$ZIZMOR_AMD64_SHA256" ;;
    arm64) triple="aarch64-unknown-linux-gnu"; sha="$ZIZMOR_ARM64_SHA256" ;;
  esac
  tb="zizmor-${triple}.tar.gz"
  echo "  -> zizmor v${ZIZMOR_VERSION}"
  curl -sSfL -o "/tmp/${tb}" \
    "https://github.com/zizmorcore/zizmor/releases/download/v${ZIZMOR_VERSION}/${tb}"
  verify_and_extract "/tmp/${tb}" "$sha" "zizmor"
  rm "/tmp/${tb}"
fi

if ! have gitleaks || [ "$MODE" = "--force" ]; then
  echo "  -> gitleaks v${GITLEAKS_VERSION} (no project-pinned sha; check release page)"
  tb="gitleaks_${GITLEAKS_VERSION}_linux_${ARCH/amd64/x64}.tar.gz"
  curl -sSfL -o "/tmp/${tb}" \
    "https://github.com/gitleaks/gitleaks/releases/download/v${GITLEAKS_VERSION}/${tb}"
  tar xzf "/tmp/${tb}" -C "$bindir" gitleaks
  rm "/tmp/${tb}"
fi

if ! have lefthook || [ "$MODE" = "--force" ]; then
  echo "  -> lefthook v${LEFTHOOK_VERSION} via go install (best for cross-OS)"
  if have go; then
    go install "github.com/evilmartians/lefthook@v${LEFTHOOK_VERSION}"
  else
    echo "    (go not installed — install go first or use brew)"
  fi
fi

echo
echo "Done. Run \`$0 check\` to verify the install set."
