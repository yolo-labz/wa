# Contract: research.md structural requirements

**Applies to**: `specs/001-research-bootstrap/research.md`
**Enforced by**: spec FR-001, FR-002, FR-003, FR-011, FR-012; checklist CHK026, CHK027.

This contract is the testable definition of "research is complete." Any future replay of this feature, any rebase that touches research.md, and any `/speckit:analyze` run must keep research.md compliant with every clause below.

## Required structure

```text
research.md
├── front matter line                 # Spec link, branch, date
├── ## Swarm scope and execution      # Table summarising agents and outcomes
├── ## OPEN-Q1 — <title>  [resolved]
├── ## OPEN-Q2 — <title>  [resolved]
├── ...                               # one section per OPEN question
├── ## OPEN-Qn — <title>  [resolved | unverified | contradicts blueprint]
├── ## Plugin design (revised)        # The wa-assistant template
├── ## Contradicts blueprint          # Table of conflicts and resolutions
├── ## Followups                      # Numbered list, each item actionable
└── ## Sources (consolidated)         # Bullet list of every URL referenced
```

## Required content rules

1. **Every OPEN-Q section MUST have a `[status]` flag in its heading**, drawn from `{[resolved], [unverified], [contradicts blueprint], [resolved — confirms blueprint], [resolved — corrects prior research]}`. The status flag is the single source of truth for the resolution lifecycle.

2. **Every OPEN-Q section MUST contain at least one inline `https://` citation**. Bare claims are forbidden. Verify with `awk '/^## OPEN-Q/{section=$0; count[section]=0} /https?:\/\//{count[section]++} END{for (s in count) if (count[s]==0) print "MISSING CITATIONS: " s}' research.md`.

3. **Every OPEN-Q section that overturns a CLAUDE.md decision MUST also appear in the `## Contradicts blueprint` table** with the columns `CLAUDE.md says | Research says | Resolution`. This ensures contradictions are surfaced at least twice — once at the Q level and once in the consolidated table — so a reader cannot miss them.

4. **Every recommendation backed by a source the agent could not directly verify MUST carry the literal string `UNVERIFIED`** in the relevant clause. Example: "the Channels socket path is `~/.run/claude/channels/wa.sock` (UNVERIFIED — fetch <https://docs.claude.com/en/docs/claude-code/channels-reference> from the next session)." This was added in response to the agent `a09d6041602329c86` false-negative incident where Channels were reported nonexistent.

5. **No section may invent code, file paths, line numbers, or schema fields**. Any verbatim block must either (a) be wrapped in `> "..."` with an inline source URL, or (b) be a code sketch labelled "to land in feature N" with no claim of upstream provenance.

6. **The `## Sources (consolidated)` section MUST list every distinct URL referenced anywhere in the document**, deduplicated. This is the audit trail for FR-002.

7. **The `## Followups` section MUST be empty if and only if no action items remain**. If actions remain, each one MUST be numbered and self-contained (a task readable in isolation).

## Quantitative thresholds

- **Minimum citation count**: ≥ 1 per OPEN-Q section. Current: every section has ≥ 1; the document total is 37.
- **Minimum verbatim quote count for "Contradicts blueprint" rows**: ≥ 1 source per contradicting row. Current: 3 contradictions, 3 cited sources.
- **Maximum `[NEEDS CLARIFICATION]` markers**: 3 (per spec template policy). Current: 0.

## Validation procedure

```sh
# 1. Section count matches OPEN-Q count
specs=$(grep -c '^## OPEN-Q' specs/001-research-bootstrap/research.md)
test "$specs" -ge 5 || { echo "FAIL: too few OPEN-Q sections"; exit 1; }

# 2. Every OPEN-Q has at least one https:// link before the next OPEN-Q
awk '
  /^## OPEN-Q/  { if (s && c==0) { print "FAIL: " s " has 0 citations"; rc=1 }
                  s=$0; c=0; next }
  /https?:\/\// { c++ }
  END           { if (s && c==0) { print "FAIL: " s " has 0 citations"; rc=1 }
                  exit rc }
' specs/001-research-bootstrap/research.md

# 3. No NEEDS CLARIFICATION markers leak through
! grep -q '\[NEEDS CLARIFICATION' specs/001-research-bootstrap/research.md \
  || { echo "FAIL: unresolved clarifications remain"; exit 1; }

# 4. The Sources section exists and is non-empty
grep -A1 '^## Sources' specs/001-research-bootstrap/research.md | grep -q '^- '
```

A passing run prints nothing and exits 0. The four checks above are the literal interpretation of FR-001, FR-002, FR-003, and FR-012.
