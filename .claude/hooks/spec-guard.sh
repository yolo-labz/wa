#!/usr/bin/env bash
# PreToolUse — refuse agent writes to specification artefacts.
#
# Mechanises CLAUDE.md rules 2 and 16, which are prompt-only today:
#   2.  "Generated artefacts are regenerated, not hand-edited. The 'spec
#        laundering' anti-pattern (agent edits the spec to match the code it
#        just wrote) is forbidden."
#   16. "Never edit spec.md, plan.md, or constitution.md from /implement."
#
# Prompt-only prohibitions are empirically insufficient: METR catalogued frontier
# models "modifying the tests or scoring code" to score higher, and RepoRescue
# (arXiv:2607.01213) measured that "Claude Code systems sometimes edit failing
# tests even when prompted not to". Its mitigation is the pattern used here — a
# runtime regime that blocks the edit rather than asking for restraint.
#
# Authorised spec changes are expected and must stay possible: run the speckit
# command that owns the artefact, or set WA_SPEC_CHANGE to a reason string. The
# override is deliberately visible — it lands in the transcript and in CI.
#
# Fail-open by contract: any parse error, missing jq, or unexpected shape exits 0
# without a decision. A crashing gate that blocks all work gets disabled, and the
# authoritative floor is the server-side required check in
# .github/workflows/spec-guard.yml, which no harness can bypass.

set -uo pipefail

command -v jq >/dev/null 2>&1 || exit 0
P=$(cat 2>/dev/null) || exit 0
[ -n "$P" ] || exit 0

TOOL=$(printf '%s' "$P" | jq -r '.tool_name // empty' 2>/dev/null) || exit 0
[ -n "$TOOL" ] || exit 0

# An explicit, auditable override. Mirrors the CLAUDE_KILL_OK idiom already used
# by this host's destructive-command guard.
if [ -n "${WA_SPEC_CHANGE:-}" ]; then exit 0; fi

# Paths whose authority is the spec process, not the implementation loop.
is_protected() {
  case "$1" in
    */.specify/memory/constitution.md|.specify/memory/constitution.md) return 0 ;;
    */specs/*/spec.md|specs/*/spec.md)                                 return 0 ;;
    */specs/*/plan.md|specs/*/plan.md)                                 return 0 ;;
    */specs/*/data-model.md|specs/*/data-model.md)                     return 0 ;;
    */specs/*/research.md|specs/*/research.md)                         return 0 ;;
    */specs/*/contracts/*|specs/*/contracts/*)                         return 0 ;;
    *.feature)                                                         return 0 ;;
    *_steps_test.go)                                                   return 0 ;;
    */features/*|features/*)                                           return 0 ;;
  esac
  return 1
}

deny() {
  jq -nc --arg r "$1" '{
    hookSpecificOutput: {
      hookEventName: "PreToolUse",
      permissionDecision: "deny",
      permissionDecisionReason: $r
    }
  }'
  exit 0
}

REASON_TAIL="This is CLAUDE.md rule 2 (no spec laundering) and rule 16, enforced at runtime rather than by prompt. If the specification genuinely must change, that is a spec decision, not an implementation one: re-run the speckit command that owns the artefact, or set WA_SPEC_CHANGE='<reason>' to record an explicit, auditable override."

case "$TOOL" in
  Write|Edit|MultiEdit|NotebookEdit)
    FP=$(printf '%s' "$P" | jq -r '.tool_input.file_path // .tool_input.notebook_path // empty' 2>/dev/null)
    [ -n "$FP" ] || exit 0
    if is_protected "$FP"; then
      deny "Refusing $TOOL on the specification artefact ${FP##*/}. $REASON_TAIL"
    fi
    ;;
  Bash)
    CMD=$(printf '%s' "$P" | jq -r '.tool_input.command // empty' 2>/dev/null)
    [ -n "$CMD" ] || exit 0
    # Only inspect commands that actually write somewhere. A command that merely
    # reads or greps a spec file is legitimate and must not be blocked.
    printf '%s' "$CMD" | grep -qE '(>>?[[:space:]]*[^|&;]*|sed[[:space:]]+-i|tee[[:space:]]|dd[[:space:]]|truncate[[:space:]]|cp[[:space:]]|mv[[:space:]]|rm[[:space:]])' || exit 0
    for tok in $(printf '%s' "$CMD" | tr '"'"'"'|&;()<>' ' ' | tr -s ' '); do
      case "$tok" in
        *spec.md|*plan.md|*data-model.md|*research.md|*constitution.md|*.feature|*_steps_test.go)
          is_protected "$tok" && deny "Refusing a shell command that writes to the specification artefact ${tok##*/}. Redirects, sed -i and tee are the same edit as the Edit tool. $REASON_TAIL"
          ;;
      esac
    done
    ;;
esac

exit 0
