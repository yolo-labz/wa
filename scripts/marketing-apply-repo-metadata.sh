#!/usr/bin/env bash
# scripts/marketing-apply-repo-metadata.sh
#
# Idempotently sets the marketing surface on the GitHub repo:
#   - Description (≤120 chars, capability-only framing)
#   - Topics (3–10, GitHub topic-discovery surface)
#
# Run from an authenticated `gh` shell (gh auth login first). Safe to re-run;
# `gh api` PATCH/PUT are idempotent on identical payloads. Source of truth for
# the marketing copy is README.md `## Capability` and `## How wa compares`.
#
# Provenance: shipped with PR #171 (feat(marketing): hero SVG + capability
# statement + comparison + asciinema + OG card).

set -euo pipefail

REPO="${REPO:-yolo-labz/wa}"

DESCRIPTION="Persistent WhatsApp daemon over Unix socket. SLSA L2 signed. Allowlist + rate limiter. Sub-second warm-call latency."

# 8 topics, GitHub topic-discovery surface. Order does not matter; GitHub stores
# them lowercase + sorted on retrieval.
TOPICS_JSON='{"names":["whatsapp","golang","daemon","claude-code","whatsmeow","slsa","supply-chain","sigstore"]}'

echo "→ patching description on ${REPO}"
gh api -X PATCH "repos/${REPO}" -f description="${DESCRIPTION}" --jq '.description'

echo "→ putting topics on ${REPO}"
printf '%s' "${TOPICS_JSON}" | gh api -X PUT "repos/${REPO}/topics" --input - --jq '.names | join(", ")'

echo "✓ done"
