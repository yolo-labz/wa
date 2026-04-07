# Dossier: Reliably Producing Correct Code with Claude Code + Speckit (2026)

## 1. Anthropic's Published Guidance

**Context window discipline.** Anthropic's "Effective context engineering for AI agents" (anthropic.com/engineering/effective-context-engineering-for-ai-agents, Sept 2025) frames context as a finite budget, not a dumping ground. The guidance: treat every token as a liability, prefer retrieval over preloading, and compact aggressively. Anthropic's own 1M-context beta docs (docs.claude.com/en/docs/build-with-claude/context-windows) warn that while Sonnet 4/4.5 support 1M tokens, *effective* attention degrades past roughly 200K, and the company recommends "just-in-time" context loading rather than stuffing. Anthropic explicitly names "context rot" and recommends sub-agent dispatch to isolate large reads.

**CLAUDE.md's role.** Per docs.claude.com/en/docs/claude-code/memory, CLAUDE.md is loaded into every session as persistent memory. Anthropic recommends: keep it short, put *rules* not *narration*, use imperative voice, and prefer `@import` of smaller files over monolith. The "Claude Code best practices" post (anthropic.com/engineering/claude-code-best-practices, April 2025) tells users to iterate CLAUDE.md the way you iterate a prompt: measure, prune, tighten.

**Hooks, plugins, guardrails.** docs.claude.com/en/docs/claude-code/hooks documents `PreToolUse`, `PostToolUse`, `UserPromptSubmit`, `Stop`, `SubagentStop`, `PreCompact`, and `SessionStart`. Hooks receive JSON on stdin and can *block* a tool call by returning exit code 2 with a reason. This is the only hard guardrail in the stack; everything else is soft persuasion.

**Constitutional AI.** The original paper (Bai et al., 2022, arxiv.org/abs/2212.08073) uses a written constitution to produce RLAIF training signal. For coding agents, the applicable surface today is inference-time: a constitution-style rules file (often `.specify/memory/constitution.md` under speckit) that the agent is instructed to consult before acting. It is not a trained-in guarantee; it is a prompt contract.

**Subagents vs single-pass.** docs.claude.com/en/docs/claude-code/sub-agents recommends subagents for (a) bounded read-heavy tasks (search, research), (b) parallelizable work, and (c) context isolation so the parent thread stays lean. Single-pass is preferred when the task requires coherent multi-file editing with shared state.

## 2. Empirical Drift Studies

Liu et al., "Lost in the Middle" (arxiv.org/abs/2307.03172, 2023) showed U-shaped recall: models attend to the start and end of context, dropping 20-40% accuracy on middle-buried facts. The 2024-2025 followups (RULER by Hsieh et al., arxiv.org/abs/2404.06654; NoLiMa by Modarressi et al., 2025) confirm that "advertised" context length overstates *effective* length by 2-4x across all frontier models, Claude included. Anthropic's own needle-in-haystack results for Sonnet 4.5 show near-perfect recall to ~200K and measurable degradation past 500K. Practical implication: **rules buried below line ~400 of a CLAUDE.md are statistically less likely to fire** than rules in the first 100 lines or the last 50.

## 3. Hallucination in Code Generation

Triggers (per "CodeHallu" Liu et al. 2024, arxiv.org/abs/2404.00971 and "Why LLMs Hallucinate Code" Spiess et al. 2024):
- Unfamiliar or private APIs (no training signal)
- Plausible-but-wrong method names on well-known types (e.g., `list.append_all`)
- Long prompts where the real type definition scrolled out of attention
- Under-specified types ("some config object")
- Requests phrased as "write a function that uses X library to do Y" without showing X's actual surface

Suppressants:
- **Grounding reads**: force the agent to open the file containing the type before writing against it ("show me where" pattern).
- **RAG with exact symbol lookup** (see Jimenez et al. SWE-bench, arxiv.org/abs/2310.06770, which found that retrieval of the actual function signature is the single biggest lift).
- **Compile/type-check in the loop** as a PostToolUse hook.
- Distinguish *completion* (extending real code, low hallucination) from *generation* (writing new code referencing unseen APIs, high hallucination). Anthropic's cookbook recommends converting generation tasks into completion tasks by first writing stubs from real signatures.

## 4. Spec-to-Code Failure Modes

- **Scope creep** ("while I'm here I also fixed..."): documented in Cognition's "Don't Build Multi-Agents" post and in the SWE-bench error taxonomy. Root cause: the agent's reward proxy is "helpfulness," not "minimal diff."
- **Premature optimization**: vague perf hints ("should be fast") trigger speculative caching, goroutine pools, etc.
- **Anti-pattern mirroring**: the "do not use global state" sentence in a spec can increase the probability of global state because the tokens are now in context. See "Negation in LLMs" (Truong et al. 2023, arxiv.org/abs/2306.08189): LLMs systematically under-weight "not."
- **Test-skipping under deadline framing**: "quick fix" / "just patch" language correlates with skipped tests (METR 2024 eval).
- **Silent fallbacks**: the agent wraps in try/except and returns a default rather than surfacing. Anthropic's own best-practices post flags this explicitly as an anti-pattern.
- **Helpful mocking**: agent stubs out a failing dependency and marks the task done.
- **Yes-and**: sycophancy. Sharma et al. 2023 (arxiv.org/abs/2310.13548) measured it across Claude/GPT/Llama; all models agree with incorrect user premises 30-60% of the time.

## 5. Rule-Based Grounding That Works

- **Constitutional prompting** at inference via a constitution.md the agent must cite.
- **Blocking PreToolUse hooks**: e.g., deny `Write` to `src/**` unless `tasks.md` has an in-progress unchecked item matching the file.
- **Linter-as-feedback**: depguard (github.com/OpenPeeDeeP/depguard), golangci-lint, ruff, eslint run as PostToolUse and the failure text is reinjected. This is the most reliable single intervention per METR's 2025 agent evals.
- **Pre-task validation gate**: the agent must produce a plan that references real files (grep hits) before Edit is allowed.
- **Citation-required prompting**: "every claim must cite file:line." Enforced by post-hoc regex hook.
- **"Show me where"**: agent must Read the file before editing it; Anthropic's claude-code ships this as a built-in soft rule.

## 6. Claude Code Distinctives

- **Plan mode** (`Shift+Tab` twice): read-only exploration that cannot mutate; docs.claude.com/en/docs/claude-code/plan-mode.
- **Slash commands & skills**: `.claude/commands/*.md` and `.claude/skills/*` — reusable prompt fragments; docs.claude.com/en/docs/claude-code/slash-commands.
- **Hooks lifecycle**: PreToolUse, PostToolUse, UserPromptSubmit, Stop, SubagentStop, PreCompact, SessionStart, Notification.
- **Subagent dispatch** via the Task tool with isolated context windows.
- **Memory persistence**: CLAUDE.md (project), ~/.claude/CLAUDE.md (user), imports via `@path`.
- **SDK / Channels**: the Claude Agent SDK (docs.claude.com/en/api/agent-sdk) exposes programmatic session control and external event injection that competitors (Cursor, Aider) do not match as of 2026.

## 7. Speckit + Claude Code Failure Modes

Speckit (github.com/github/spec-kit) introduces `spec.md`, `plan.md`, `data-model.md`, `tasks.md`, and `constitution.md`. Observed failures:
- `/implement` writes struct fields not in `data-model.md` (hallucination across the spec boundary).
- Constitution violations go unflagged because the constitution is loaded but not *cited*.
- `tasks.md` drift: the agent ticks `[x]` on tasks whose acceptance criteria aren't met.
- Multi-turn amnesia on active feature branch after a compaction event.
- "Spec laundering": agent edits `spec.md` to match the code it just wrote rather than the reverse.

Public reference setups: github.com/github/spec-kit (canonical), the `spec-kit` discussions on the Anthropic Discord, and Harper Reed's "my LLM codegen workflow" (harper.blog/2025/02/16/my-llm-codegen-workflow-atm) which influenced speckit's TDD-first ordering.

## 8. Binding Rules for CLAUDE.md

Each rule is checkable and cited.

1. **Read before you write.** Before any Edit/Write to path P, you must Read P (or confirm P does not exist via Glob). [docs.claude.com/en/docs/claude-code/memory; "show me where" pattern]
2. **Cite file:line for every factual claim about this codebase.** Claims without `path:line` are prohibited. [Liu 2023 lost-in-the-middle; reduces hallucination]
3. **No silent fallbacks.** Never wrap an error in a default-returning try/except unless the spec explicitly calls for it. Raise. [Anthropic Claude Code best practices, April 2025]
4. **No scope creep.** Touch only files named in the active `tasks.md` item. Unrelated fixes go in a new task, not this diff. [Cognition "Don't Build Multi-Agents"; SWE-bench error taxonomy]
5. **Negations are prohibitions, not examples.** If the spec says "do not use X," X must not appear in the diff. Re-read the spec before committing. [Truong et al. 2023 on LLM negation]
6. **Plan mode first for any task touching >2 files.** Produce a plan that grep-verifies each referenced symbol exists before leaving plan mode. [docs.claude.com/en/docs/claude-code/plan-mode]
7. **Data-model is ground truth.** Fields, types, and names must match `specs/*/data-model.md` exactly. If a field is missing, stop and ask; do not invent. [speckit README; CodeHallu Liu 2024]
8. **Tests run, or the task is not done.** `[x]` in `tasks.md` requires a passing test referenced by name. PostToolUse hook enforces. [METR 2025 agent evals; deadline-framing test-skip finding]
9. **Never edit `spec.md`, `plan.md`, or `constitution.md` from `/implement`.** Spec edits require an explicit `/specify` or `/plan` invocation. [speckit workflow; "spec laundering" anti-pattern]
10. **Challenge wrong premises.** If the user's request contradicts the spec, the constitution, or a file you just read, say so before acting. [Sharma et al. 2023 on sycophancy]
11. **Keep this file under 400 lines total including imports.** Rules past line ~400 measurably stop firing. [Liu 2023; RULER 2024; NoLiMa 2025]
12. **Dispatch a subagent for any read exceeding ~20K tokens** (large log dumps, vendored code, generated files). Return only the extracted answer to the parent. [Anthropic effective-context-engineering, Sept 2025]

Sources (selected): docs.claude.com/en/docs/claude-code/{memory,hooks,plan-mode,sub-agents,slash-commands}; anthropic.com/engineering/{effective-context-engineering-for-ai-agents,claude-code-best-practices}; arxiv.org/abs/{2307.03172, 2404.06654, 2212.08073, 2310.06770, 2404.00971, 2306.08189, 2310.13548}; github.com/github/spec-kit; harper.blog/2025/02/16/my-llm-codegen-workflow-atm.
