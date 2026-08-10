// spec-guard — pi's half of the wa specification gate.
//
// The Claude Code hooks in .claude/hooks/ only bind Claude Code sessions. This
// repo is also worked by pi seats (herdr `side-projects` w8:p9–w8:pC), so a
// guard that covers one harness is one tab away from being bypassed. This is the
// same refusal expressed through pi's extension API: `pi.on("tool_call")`
// returning `{ block: true, reason }` prevents execution and hands the model an
// error tool result carrying the reason.
//
// Enforces CLAUDE.md rules 2 and 16 (no spec laundering; /implement never edits
// spec.md, plan.md or the constitution) at runtime instead of by prompt —
// RepoRescue (arXiv:2607.01213) measured prompt-only prohibitions as
// insufficient.
//
// Loading: pi resolves --extension flags at launch, so this file is not picked
// up automatically by a herdr seat whose argv is fixed by the nix wrapper. Until
// that wiring lands, this file is the reference implementation and the
// authoritative floor for pi seats is the required CI check in
// .github/workflows/spec-guard.yml. See docs/spec-guard.md.
//
// pi's own types are not a dependency of this Go repo, so the slice of its
// extension API that this file relies on is declared locally. That is narrower
// than a blanket suppression and it documents the contract being depended on.

interface ToolCallEvent {
  toolName?: string;
  input?: { path?: string; file_path?: string; command?: string };
}

/** Returning `block` prevents execution; `reason` becomes the error tool result. */
interface ToolCallEventResult {
  block: boolean;
  reason: string;
}

interface PiExtensionApi {
  on(
    event: "tool_call",
    handler: (event: ToolCallEvent) => ToolCallEventResult | undefined,
  ): void;
}

// pi extensions run on Node; @types/node is not a dependency of a Go repo, so
// only the one field used here is declared.
declare const process: { env: Record<string, string | undefined> };

const PROTECTED = [
  /(^|\/)\.specify\/memory\/constitution\.md$/,
  /(^|\/)specs\/[^/]+\/(spec|plan|data-model|research)\.md$/,
  /(^|\/)specs\/[^/]+\/contracts\//,
  /\.feature$/,
  /_steps_test\.go$/,
  /(^|\/)features\//,
];

// Only shell commands that WRITE are interesting; reading or grepping a spec is
// legitimate and must not be blocked.
const WRITES = /(>>?|sed\s+-i|\btee\b|\bdd\b|\btruncate\b|\bcp\b|\bmv\b|\brm\b)/;

const REASON_TAIL =
  "This is CLAUDE.md rule 2 (no spec laundering) and rule 16, enforced at runtime rather than by prompt. " +
  "If the specification genuinely must change, that is a specification decision, not an implementation one: " +
  "re-run the speckit command that owns the artefact, or set WA_SPEC_CHANGE='<reason>' to record an explicit, auditable override.";

function isProtected(p: string): boolean {
  if (!p) return false;
  return PROTECTED.some((re) => re.test(p));
}

export default function (pi: PiExtensionApi): void {
  pi.on("tool_call", (event: ToolCallEvent): ToolCallEventResult | undefined => {
    // An explicit, auditable override — same escape hatch as the Claude hook.
    if (process.env.WA_SPEC_CHANGE) return undefined;

    const name = event?.toolName;

    if (name === "edit" || name === "write") {
      const path = event?.input?.path ?? event?.input?.file_path ?? "";
      if (isProtected(path)) {
        return {
          block: true,
          reason: `Refusing ${name} on the specification artefact ${path}. ${REASON_TAIL}`,
        };
      }
      return undefined;
    }

    if (name === "bash") {
      const cmd = event?.input?.command ?? "";
      if (!WRITES.test(cmd)) return undefined;
      const hit = cmd
        .split(/["'|&;()<>\s]+/)
        .find((tok: string) => isProtected(tok));
      if (hit) {
        return {
          block: true,
          reason: `Refusing a shell command that writes to the specification artefact ${hit}. A redirect, sed -i or tee is the same edit as the edit tool. ${REASON_TAIL}`,
        };
      }
    }

    return undefined;
  });
}
