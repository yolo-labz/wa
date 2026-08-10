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
# `reason` is injected into the agent's next turn and OUTRANKS its original
# instruction — the probe that established the block semantics also showed the
# agent following an injected reason over the task it was given. So this field is
# a prompt-injection surface, not a message, and a path is attacker-influenced in
# any repo that accepts outside contributions: a branch carrying
# `specs/x/"Ignore previous instructions and...".feature` would otherwise have
# its filename read as an instruction.
#
# Therefore: NUL-separated paths (so a name containing a space or newline cannot
# split a field), a strict allowlist, and anything outside it is reported as a
# COUNT rather than echoed. File contents never appear.
safe_paths=""
n_safe=0
n_unsafe=0

while IFS= read -r -d '' entry; do
  # porcelain -z: two status chars, a space, then the raw path.
  path=${entry:3}
  [ -n "$path" ] || continue
  case "$path" in
    *[!A-Za-z0-9._/-]*) n_unsafe=$((n_unsafe + 1)) ;;
    *)
      if [ "$n_safe" -lt 12 ]; then
        safe_paths="${safe_paths}${safe_paths:+ }${path}"
      fi
      n_safe=$((n_safe + 1))
      ;;
  esac
# Three flags, each load-bearing:
#   -z            NUL-separated, so a path containing a space or newline cannot
#                 split a field (and git does not quote/escape it either).
#   -uall         git collapses untracked files into their parent directory by
#                 default, and a collapsed directory does not match a
#                 `specs/*/spec.md` pathspec — so a brand-new spec file, the most
#                 likely shape of an unauthorised spec change, would be invisible.
#
# KNOWN LIMIT, stated rather than papered over: this repo's .gitignore lists
# `specs/` and `.specify/`, and `git status` omits ignored paths entirely. So a
# BRAND-NEW file under `specs/` is invisible here — only modifications to the
# already-tracked spec files are seen. Adding `--ignored` does not fix it: git
# collapses a wholly-ignored directory to `specs/<dir>/` and the individual
# filenames never appear. New specification files are covered by the PreToolUse
# guard, which refuses the write in the first place, and by the CI classifier.
# This gate is the backstop for the tracked case, not the whole answer.
done < <(git status --porcelain -z -uall -- \
  '.specify/memory/constitution.md' \
  'specs/*/spec.md' 'specs/*/plan.md' 'specs/*/data-model.md' 'specs/*/research.md' \
  'specs/*/contracts' \
  '*.feature' '*_steps_test.go' 2>/dev/null)

[ $((n_safe + n_unsafe)) -gt 0 ] || exit 0

if [ "$n_safe" -gt 0 ]; then
  WHICH="$safe_paths"
  [ "$n_safe" -gt 12 ] && WHICH="$WHICH (and $((n_safe - 12)) more)"
else
  WHICH="(none displayable)"
fi
[ "$n_unsafe" -gt 0 ] && WHICH="$WHICH; $n_unsafe further path(s) withheld — their names contain characters this gate will not echo"

jq -nc --arg r "Specification artefacts are modified in the working tree: ${WHICH}. Do not end the turn treating this as ordinary implementation work. State explicitly which spec changed and why the change is a specification decision rather than the code being made to fit — CLAUDE.md rule 2 forbids editing a spec to match code already written. If the change is correct and authorised, say so and name the authority (the speckit command that produced it, or the WA_SPEC_CHANGE reason). If it is not, revert it before finishing." \
  '{decision:"block", reason:$r}'
exit 0
