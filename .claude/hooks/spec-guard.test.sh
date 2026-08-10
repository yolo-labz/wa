#!/usr/bin/env bash
# Discrimination tests for the spec gate. A guard that blocks everything and a
# guard that blocks nothing both "pass" a smoke test — each case below therefore
# pairs a block with the near-miss that must NOT block.
#
# Run: .claude/hooks/spec-guard.test.sh   (exit 0 = all pass)

set -uo pipefail
cd "$(dirname "$0")/../.." || exit 1
GUARD=.claude/hooks/spec-guard.sh
STOP=.claude/hooks/stop-spec-gate.sh
pass=0; fail=0

check() { # check <name> <expect: deny|allow> <json>
  local name="$1" expect="$2" json="$3" out got
  out=$(printf '%s' "$json" | WA_SPEC_CHANGE= "$GUARD" 2>/dev/null)
  if printf '%s' "$out" | grep -q '"permissionDecision"[[:space:]]*:[[:space:]]*"deny"'; then got=deny; else got=allow; fi
  if [ "$got" = "$expect" ]; then pass=$((pass+1)); printf '  ok   %-52s %s\n' "$name" "$got"
  else fail=$((fail+1)); printf '  FAIL %-52s want=%s got=%s\n' "$name" "$expect" "$got"; fi
}

echo "PreToolUse — protected artefacts must be refused"
check "Edit spec.md"                deny  '{"tool_name":"Edit","tool_input":{"file_path":"specs/110-rest-primary-adapter/spec.md"}}'
check "Edit plan.md"                deny  '{"tool_name":"Edit","tool_input":{"file_path":"specs/109-dokku-deploy/plan.md"}}'
check "Edit data-model.md"          deny  '{"tool_name":"Edit","tool_input":{"file_path":"specs/016-code-quality-audit/data-model.md"}}'
check "Write constitution.md"       deny  '{"tool_name":"Write","tool_input":{"file_path":".specify/memory/constitution.md"}}'
check "Write a .feature"            deny  '{"tool_name":"Write","tool_input":{"file_path":"features/pairing.feature"}}'
check "Edit a step definition"      deny  '{"tool_name":"Edit","tool_input":{"file_path":"internal/bdd/pair_steps_test.go"}}'
check "Edit a contracts/ file"      deny  '{"tool_name":"Edit","tool_input":{"file_path":"specs/110-rest-primary-adapter/contracts/openapi.yaml"}}'

echo "PreToolUse — ordinary work must NOT be refused (discrimination)"
check "Edit implementation .go"     allow '{"tool_name":"Edit","tool_input":{"file_path":"internal/app/router.go"}}'
check "Edit an ordinary _test.go"   allow '{"tool_name":"Edit","tool_input":{"file_path":"internal/app/router_test.go"}}'
check "Edit tasks.md (agent-owned)" allow '{"tool_name":"Edit","tool_input":{"file_path":"specs/016-code-quality-audit/tasks.md"}}'
check "Edit README"                 allow '{"tool_name":"Write","tool_input":{"file_path":"README.md"}}'
check "Read a spec"                 allow '{"tool_name":"Read","tool_input":{"file_path":"specs/109-dokku-deploy/spec.md"}}'

echo "PreToolUse — shell bypasses"
check "bash sed -i on spec.md"      deny  '{"tool_name":"Bash","tool_input":{"command":"sed -i s/a/b/ specs/109-dokku-deploy/spec.md"}}'
check "bash redirect into spec.md"  deny  '{"tool_name":"Bash","tool_input":{"command":"echo hi > specs/109-dokku-deploy/spec.md"}}'
check "bash tee into constitution"  deny  '{"tool_name":"Bash","tool_input":{"command":"echo x | tee .specify/memory/constitution.md"}}'
check "bash grep a spec (read)"     allow '{"tool_name":"Bash","tool_input":{"command":"grep -n FR- specs/109-dokku-deploy/spec.md"}}'
check "bash cat a spec (read)"      allow '{"tool_name":"Bash","tool_input":{"command":"cat specs/109-dokku-deploy/spec.md"}}'
check "bash go build"               allow '{"tool_name":"Bash","tool_input":{"command":"go build ./..."}}'

echo "PreToolUse — the override must work"
out=$(printf '%s' '{"tool_name":"Edit","tool_input":{"file_path":"specs/109-dokku-deploy/spec.md"}}' \
  | WA_SPEC_CHANGE="deliberate: FR-7 clarified before implementing" "$GUARD" 2>/dev/null)
if [ -z "$out" ]; then pass=$((pass+1)); printf '  ok   %-52s allow\n' "WA_SPEC_CHANGE overrides the refusal"
else fail=$((fail+1)); printf '  FAIL %-52s override did not apply\n' "WA_SPEC_CHANGE overrides the refusal"; fi

echo "Stop — the loop guard is what terminates the block"
out=$(printf '%s' '{"stop_hook_active":true}' | "$STOP" 2>/dev/null)
if [ -z "$out" ]; then pass=$((pass+1)); printf '  ok   %-52s no block\n' "re-entry (stop_hook_active=true) short-circuits"
else fail=$((fail+1)); printf '  FAIL %-52s blocked on re-entry (infinite loop)\n' "re-entry short-circuits"; fi

out=$(printf '%s' '{"stop_hook_active":false}' | "$STOP" 2>/dev/null)
if git diff --quiet -- specs .specify 2>/dev/null && [ -z "$(git status --porcelain -- specs .specify 2>/dev/null)" ]; then
  if [ -z "$out" ]; then pass=$((pass+1)); printf '  ok   %-52s no block\n' "clean tree does not block a stop"
  else fail=$((fail+1)); printf '  FAIL %-52s blocked with a clean tree\n' "clean tree does not block a stop"; fi
else
  printf '  skip %-52s (spec artefacts dirty in this tree)\n' "clean-tree case"
fi

echo
echo "pass=$pass fail=$fail"
[ "$fail" -eq 0 ]
