#!/usr/bin/env bash
# Conventional Commits validator. Spec: https://www.conventionalcommits.org/en/v1.0.0/
# Usage: commit-msg-check.sh <path-to-COMMIT_EDITMSG>
set -euo pipefail

msg_file="${1:?missing commit message file path}"
# First non-comment, non-empty line is the subject.
subject=$(grep -v '^#' "$msg_file" | sed '/^[[:space:]]*$/d' | head -n1 || true)

if [ -z "$subject" ]; then
  echo "commit-msg: empty commit message" >&2
  exit 1
fi

# type(scope)!: description     scope and ! are optional
# allowed types match Angular conventional commits + a few project-specific ones.
pattern='^(feat|fix|docs|style|refactor|perf|test|build|ci|chore|revert)(\([a-z0-9._/-]+\))?!?: .{1,}$'

if ! [[ "$subject" =~ $pattern ]]; then
  cat >&2 <<EOF
commit-msg: subject does not follow Conventional Commits.

  got:      $subject

  expected: <type>(<scope>)?!?: <description>
  types:    feat, fix, docs, style, refactor, perf, test, build, ci, chore, revert
  example:  feat(adapter/whatsmeow): add pairing flow

see https://www.conventionalcommits.org/en/v1.0.0/
EOF
  exit 1
fi

if [ ${#subject} -gt 72 ]; then
  echo "commit-msg: subject is ${#subject} chars; keep it <=72" >&2
  exit 1
fi
