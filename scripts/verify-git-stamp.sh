#!/bin/sh
# verify-git-stamp.sh — refuse to stamp a build whose .git does not describe
# the sources actually present in the build context (wa#365).
#
# A build context can carry a .git that is stale relative to its sources: a
# cached checkout, a foreign worktree, or a copy pushed by a deploy hook. The
# Dockerfile derives VERSION/COMMIT from that .git when no build args are
# passed (the Dokku `git push` path passes none), so a stale .git yields a
# banner that is confidently wrong — an image reporting a commit weeks behind
# the code it is actually running. That is worse than "dev", because "dev"
# is obviously unstamped while a stale SHA looks like a verified deploy.
#
# Files excluded from the context by .dockerignore are absent for the build
# *and* for this check, so absence proves nothing and is skipped. A file that
# IS present must hash to the blob recorded in <commit>.
#
# usage: verify-git-stamp.sh <commit>
#   exit 0 — every present tracked file matches <commit>
#   exit 1 — at least one file differs (stale or foreign .git)
#   exit 2 — usage error / no repository / nothing to verify against
set -eu

commit=${1-}
case "$commit" in
'' | unknown | dev)
  echo "verify-git-stamp: refusing to verify against '$commit'" >&2
  exit 2
  ;;
esac

if ! git rev-parse --git-dir >/dev/null 2>&1; then
  echo "verify-git-stamp: no git repository in this build context" >&2
  exit 2
fi

mismatch_file=$(mktemp)
trap 'rm -f "$mismatch_file"' EXIT INT TERM

# One line per present file whose content differs from <commit>. Written to a
# file rather than piped so a long mismatch list cannot SIGPIPE the producer.
# shellcheck disable=SC2030,SC2031
git ls-tree -r "$commit" | while IFS='	' read -r meta path; do
  blob=${meta##* }
  [ -e "$path" ] || continue
  actual=$(git hash-object -- "$path" 2>/dev/null || echo unreadable)
  [ "$actual" = "$blob" ] || printf '%s\n' "$path"
done >"$mismatch_file"

if [ -s "$mismatch_file" ]; then
  echo "verify-git-stamp: the sources in this context do not match $commit:" >&2
  head -n 5 "$mismatch_file" | sed 's/^/  /' >&2
  echo "verify-git-stamp: $commit is a stale or foreign .git. Stamp explicitly instead:" >&2
  echo "  --build-arg COMMIT=\$(git rev-parse HEAD) --build-arg VERSION=\$(git describe --tags --always)" >&2
  exit 1
fi

exit 0
