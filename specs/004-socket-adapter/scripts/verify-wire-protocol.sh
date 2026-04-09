#!/usr/bin/env bash
# verify-wire-protocol.sh — checks that every error code in the wire-protocol
# contract has a matching Go constant in errcodes.go, and vice versa.
set -euo pipefail

REPO_ROOT="$(cd "$(dirname "$0")/../../.." && pwd)"
CONTRACT="$REPO_ROOT/specs/004-socket-adapter/contracts/wire-protocol.md"
ERRCODES="$REPO_ROOT/internal/adapters/primary/socket/errcodes.go"

# Extract numeric codes from the markdown table rows (| `-32NNN` | pattern).
md_codes=$(grep -oE '`-32[0-9]+`' "$CONTRACT" | tr -d '`' | sort -u)

# Extract numeric codes from Go constants (ErrorCode = -32NNN).
go_codes=$(grep -oE 'ErrorCode = -32[0-9]+' "$ERRCODES" | grep -oE -- '-32[0-9]+' | sort -u)

# Filter: only check codes in the -32000..-32005 and -32600..-32700 ranges
# that feature 004 owns (skip reserved ranges like -32006..-32099).
owned_md_codes=$(echo "$md_codes" | grep -E '^-32(00[0-5]|60[0-3]|700)$' || true)

rc=0
for code in $owned_md_codes; do
    if ! echo "$go_codes" | grep -qxF -- "$code"; then
        echo "MISSING in Go: $code (present in wire-protocol.md)"
        rc=1
    fi
done

for code in $go_codes; do
    if ! echo "$md_codes" | grep -qxF -- "$code"; then
        echo "MISSING in contract: $code (present in errcodes.go)"
        rc=1
    fi
done

if [ "$rc" -eq 0 ]; then
    echo "OK: all error codes match between wire-protocol.md and errcodes.go"
fi
exit "$rc"
