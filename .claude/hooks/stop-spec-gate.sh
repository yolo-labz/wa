#!/usr/bin/env bash
# Stop — refuse a "done" claim while specification artefacts sit modified.
#
# The PreToolUse guard blocks the edit; this catches the case where a spec
# artefact ended up modified anyway (an authorised WA_SPEC_CHANGE run, a stray
# formatter, a sibling harness without the guard) and the turn is about to end
# without that being said out loud.
#
# Block contract, measured on Claude Code 2.1.219 (10/08/2026): a Stop hook
# blocks by printing {"decision":"block","reason":…} to stdout and exiting 0 —
# NOT by exiting 2. The assistant receives `reason`, continues, and Stop fires
# again when it next tries to finish.
#
# The `stop_hook_active` short-circuit below is mandatory, not defensive. A probe
# run on this host recorded invocation 1 with stop_hook_active=false (blocked,
# and the agent obeyed the reason) and invocation 2 with stop_hook_active=true
# (short-circuited, clean exit). Without that guard the loop has no terminator.
#
# Fail-open by contract: every error path exits 0 with no JSON.

set -uo pipefail

command -v jq >/dev/null 2>&1 || exit 0
P=$(cat 2>/dev/null) || exit 0
[ -n "$P" ] || exit 0

# Loop guard — if this hook already blocked once this turn, let the agent stop.
if [ "$(printf '%s' "$P" | jq -r '.stop_hook_active // false' 2>/dev/null)" = "true" ]; then
  exit 0
fi

command -v git >/dev/null 2>&1 || exit 0
git rev-parse --is-inside-work-tree >/dev/null 2>&1 || exit 0

# Modified or newly added specification artefacts in the working tree.
DIRTY=$(git status --porcelain -- \
  '.specify/memory/constitution.md' \
  'specs/*/spec.md' 'specs/*/plan.md' 'specs/*/data-model.md' 'specs/*/research.md' \
  'specs/*/contracts' \
  '*.feature' '*_steps_test.go' 2>/dev/null | awk '{print $NF}' | head -12)

[ -n "$DIRTY" ] || exit 0

LIST=$(printf '%s' "$DIRTY" | tr '\n' ' ')

jq -nc --arg r "Specification artefacts are modified in the working tree: ${LIST}. Do not end the turn treating this as ordinary implementation work. State explicitly which spec changed and why the change is a specification decision rather than the code being made to fit — CLAUDE.md rule 2 forbids editing a spec to match code already written. If the change is correct and authorised, say so and name the authority (the speckit command that produced it, or the WA_SPEC_CHANGE reason). If it is not, revert it before finishing." \
  '{decision:"block", reason:$r}'
exit 0
