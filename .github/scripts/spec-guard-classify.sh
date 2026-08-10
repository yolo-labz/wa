#!/usr/bin/env bash
# spec-guard classifier — decides whether a diff is an undeclared
# specification + implementation change.
#
# Extracted from .github/workflows/spec-guard.yml so it can be tested. The
# inline version passed on three PRs (#338, #339, #340) without its blocking
# branch ever executing: every one of those diffs failed the AND, so the job was
# only ever proving it does not false-positive. A gate whose refusal path has
# never run is not evidence, and that is the exact "reports nothing" failure the
# BDD work is meant to avoid.
#
# Reads a newline-separated file list on stdin.
#   argv[1] — comma-separated PR labels (may be empty)
# Prints a report and exits:
#   0  allowed  (not both categories, or declared with the spec-change label)
#   1  refused  (spec + implementation together, undeclared)

set -uo pipefail

LABELS="${1:-}"

SPEC_RE='^\.specify/memory/constitution\.md$|^specs/[^/]+/(spec|plan|data-model|research)\.md$|^specs/[^/]+/contracts/|\.feature$|_steps_test\.go$'
# Implementation = Go source the specification is supposed to constrain. Step
# definitions are spec-side, so they are subtracted here rather than matched
# twice; go.mod/go.sum are neither (they do not end in .go).
IMPL_RE='\.go$'

changed=$(cat)

spec=$(printf '%s\n' "$changed" | grep -E "$SPEC_RE" || true)
impl=$(printf '%s\n' "$changed" | grep -E "$IMPL_RE" | grep -vE '_steps_test\.go$' || true)

n_spec=$(printf '%s' "$spec" | grep -c . || true)
n_impl=$(printf '%s' "$impl" | grep -c . || true)

echo "spec artefacts: $n_spec"
[ "$n_spec" -gt 0 ] && printf '%s\n' "$spec" | sed 's/^/  /'
echo "implementation: $n_impl"
[ "$n_impl" -gt 0 ] && printf '%s\n' "$impl" | sed 's/^/  /'

if [ "$n_spec" -eq 0 ] || [ "$n_impl" -eq 0 ]; then
  echo "verdict: ALLOW (not a combined spec + implementation change)"
  exit 0
fi

case ",$LABELS," in
  *,spec-change,*)
    echo "verdict: ALLOW (declared with the spec-change label)"
    exit 0
    ;;
esac

echo "verdict: REFUSE (undeclared specification + implementation change)"
exit 1
