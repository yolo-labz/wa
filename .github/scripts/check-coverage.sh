#!/usr/bin/env bash
# Spec 016 FR-020 / T072: assert minimum coverage on production-relevant
# packages. Parses `go tool cover -func=cover.out` output and rejects
# sub-threshold averages for internal/domain, internal/app, and the
# rest of internal/* (adapters). Test files, cmd/* composition roots,
# and the experimental observability package are excluded — they are
# exercised by integration tests that do not contribute to the unit-
# test coverage profile.
#
# Thresholds are passed via env vars (DOMAIN_THRESHOLD, APP_THRESHOLD,
# ADAPTERS_THRESHOLD) so the workflow file owns the policy and this
# script owns the parse logic.

set -euo pipefail

: "${DOMAIN_THRESHOLD:?required}"
: "${APP_THRESHOLD:?required}"
: "${ADAPTERS_THRESHOLD:?required}"

go tool cover -func=cover.out > coverage.txt
tail -n1 coverage.txt

avg() {
  awk -v re="$1" '$1 ~ re {gsub(/%/,"",$NF); s+=$NF; n++} END {if (n>0) print s/n; else print 0}' coverage.txt
}

domain_pct="$(avg '/internal/domain/')"
app_pct="$(avg '/internal/app/')"
adapters_pct="$(avg '/internal/adapters/')"

printf 'domain   = %s%%   threshold %s%%\n' "$domain_pct"   "$DOMAIN_THRESHOLD"
printf 'app      = %s%%   threshold %s%%\n' "$app_pct"      "$APP_THRESHOLD"
printf 'adapters = %s%%   threshold %s%%\n' "$adapters_pct" "$ADAPTERS_THRESHOLD"

fail=0
check() {
  awk -v v="$1" -v t="$2" 'BEGIN{exit !(v+0 >= t+0)}' || {
    echo "FAIL: $3 average $1% below threshold $2%"
    fail=1
  }
}
check "$domain_pct"   "$DOMAIN_THRESHOLD"   "internal/domain"
check "$app_pct"      "$APP_THRESHOLD"      "internal/app"
check "$adapters_pct" "$ADAPTERS_THRESHOLD" "internal/adapters"

exit "$fail"
