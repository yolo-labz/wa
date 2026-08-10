#!/usr/bin/env bash
# Discrimination tests for the spec-guard classifier.
#
# The first case is the one that matters: it is the refusal branch, which the
# live job had never executed across #338, #339 and #340. Everything after it is
# the near-miss set that must NOT refuse — a gate that refuses honest work gets
# switched off, and then it protects nothing.
#
# Run: .github/scripts/spec-guard-classify.test.sh   (exit 0 = all pass)

set -uo pipefail
cd "$(dirname "$0")" || exit 1
C=./spec-guard-classify.sh
pass=0
fail=0

check() { # check <name> <expect: refuse|allow> <labels> <files...>
  local name="$1" expect="$2" labels="$3"; shift 3
  local got
  if printf '%s\n' "$@" | "$C" "$labels" >/dev/null 2>&1; then got=allow; else got=refuse; fi
  if [ "$got" = "$expect" ]; then
    pass=$((pass + 1)); printf '  ok   %-56s %s\n' "$name" "$got"
  else
    fail=$((fail + 1)); printf '  FAIL %-56s want=%s got=%s\n' "$name" "$expect" "$got"
  fi
}

echo "the refusal branch — never exercised by a live PR until now"
check "spec.md + implementation .go"        refuse "" "specs/110-rest/spec.md" "internal/app/router.go"
check "constitution + implementation .go"   refuse "" ".specify/memory/constitution.md" "internal/domain/allowlist.go"
check ".feature + implementation .go"       refuse "" "features/allowlist.feature" "internal/domain/allowlist.go"
check "contracts/ + implementation .go"     refuse "" "specs/110-rest/contracts/openapi.yaml" "internal/app/router.go"

echo "the declared escape hatch"
check "same diff, spec-change label"        allow  "spec-change" "specs/110-rest/spec.md" "internal/app/router.go"
check "label among several"                 allow  "bug,spec-change,ci" "features/a.feature" "internal/app/router.go"
check "a different label does not count"    refuse "documentation" "specs/110-rest/spec.md" "internal/app/router.go"

echo "near misses that must NOT refuse"
check "spec only (no implementation)"       allow  "" "specs/110-rest/spec.md" "docs/readme.md"
check "implementation only (no spec)"       allow  "" "internal/app/router.go" "internal/domain/allowlist.go"
check "step definitions are spec-side"      allow  "" "features/a.feature" "internal/domain/a_steps_test.go"
check "go.mod/go.sum are neither"           allow  "" "features/a.feature" "go.mod" "go.sum"
check "flake.nix is neither"                allow  "" "features/a.feature" "flake.nix"
check "docs-only change"                    allow  "" "docs/spec-guard.md" "README.md"
check "ordinary _test.go is implementation" refuse "" "specs/110-rest/spec.md" "internal/app/router_test.go"
check "tasks.md is not a protected spec"    allow  "" "specs/016-audit/tasks.md" "internal/app/router.go"

echo "the real diffs from the three PRs that shipped this gate"
check "#339 (feature + steps + go.mod)"     allow  "" "features/allowlist.feature" "flake.nix" "go.mod" "go.sum" "internal/domain/allowlist_steps_test.go"
check "#340 (docs + CODEOWNERS)"            allow  "" ".github/CODEOWNERS" "docs/spec-guard.md"

echo
echo "pass=$pass fail=$fail"
[ "$fail" -eq 0 ]
