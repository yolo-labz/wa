# Architecture Decision Quality Checklist: 001-research-bootstrap

**Purpose**: Unit-test the architectural decisions documented across `CLAUDE.md`, [`spec.md`](../spec.md), [`plan.md`](../plan.md), [`research.md`](../research.md), and [`contracts/`](../contracts/) for **completeness, clarity, consistency, measurability, and traceability**. This is not a verification checklist for code (no code exists yet); it is a quality audit of the *decisions themselves* before feature 002 starts writing Go.

**Created**: 2026-04-06
**Feature**: [`spec.md`](../spec.md) · [`plan.md`](../plan.md) · [`research.md`](../research.md)
**Theme**: architecture · **Depth**: deep · **Audience**: an external contributor reading the repo cold

> Each item asks whether the requirements/decisions are *written well*, not whether the implementation exists. References use `[CLAUDE.md §...]`, `[spec FR-NNN]`, `[research §OPEN-Qn]`, `[contracts/<file>]`, `[Gap]`, `[Ambiguity]`, `[Conflict]`, or `[Assumption]`.

## Decision completeness — language, library, and runtime

- [x] CHK001 Is the chosen language pinned to a specific minimum version (Go 1.22+ vs the dev-host 1.26.1)? [Clarity] [`CLAUDE.md` §"Locked decisions"]
- [x] CHK002 Is the upstream `whatsmeow` version pin strategy documented (commit hash vs latest vs Renovate-managed pseudo-version)? [Completeness] [`research §OPEN-Q7`]
- [x] CHK003 Is the SQLite driver choice (`modernc.org/sqlite`) tied explicitly to the `CGO_ENABLED=0` cross-compile requirement? [Consistency] [`CLAUDE.md` §"Locked decisions"]
- [x] CHK004 Are all transitive dependencies the user must accept enumerated, or only the direct ones? [Gap] [`CLAUDE.md` §"Locked decisions"]
- [x] CHK005 Is "no CGO" stated as a binding rule that future features cannot relax without revisiting distribution? [Clarity] [`CLAUDE.md` §"Locked decisions"]

## Decision completeness — architecture and layering

- [x] CHK006 Are the seven port interfaces in `internal/app/ports.go` listed with full Go signatures, parameter types, and error semantics? [Completeness] [`CLAUDE.md` §"Ports"]
- [x] CHK007 Is the rule "no whatsmeow imports under `internal/{domain,app}`" stated as a hard invariant AND tied to the enforcement mechanism (`golangci-lint depguard`)? [Consistency] [`CLAUDE.md` §"Anti-patterns"] [`research §OPEN-Q7`]
- [x] CHK008 Is the boundary between `cmd/wa` (thin client) and `cmd/wad` (full composition root) defined unambiguously, including which package owns the cobra command tree? [Clarity] [`CLAUDE.md` §"Repository layout"]
- [x] CHK009 Is "EventStream is pull-based, not push-based" justified with the goroutine-lifecycle reason rather than asserted? [Clarity] [`CLAUDE.md` §"Ports"]
- [x] CHK010 Are the responsibilities of `internal/adapters/secondary/{whatsmeow,sqlitestore,memory,slogaudit}` distinct enough that two of them could not credibly own the same use case? [Consistency] [`CLAUDE.md` §"Repository layout"]
- [x] CHK011 Is the future addition of `internal/adapters/primary/{rest,mcp,channel}` documented as a deferred plan with criteria for when to add each, or only as a list of names? [Completeness] [`CLAUDE.md` §"Repository layout"]

## Decision completeness — daemon, IPC, and transport

- [x] CHK012 Is the JSON-RPC method list complete, with parameter and result schemas for each method (`send`, `sendMedia`, `markRead`, `pair`, `status`, `groups`, `subscribe`, `wait`)? [Completeness] [`CLAUDE.md` §"Daemon, IPC, single-instance"]
- [x] CHK013 Is the unix-socket path documented with the exact darwin fallback when `$XDG_RUNTIME_DIR` is unset? [Clarity] [`CLAUDE.md` §"Daemon, IPC, single-instance"]
- [x] CHK014 Is the single-instance enforcement mechanism (`flock` on the SQLite store AND the socket) stated as a hard requirement with the failure mode if it is missed (corrupted ratchet store)? [Edge Cases] [`CLAUDE.md` §"Daemon, IPC, single-instance"]
- [x] CHK015 Is the daemon's reconnection contract (delegates to whatsmeow's built-in loop, surfaces `events.Disconnected/Connected` to subscribers) defined precisely enough that a tester can write a contract test? [Measurability] [`CLAUDE.md` §"Daemon, IPC, single-instance"]
- [x] CHK016 Is the lifetime distinction between `clientCtx` (daemon-scoped, derived from `context.Background()`) and per-request contexts documented to prevent the aldinokemal-style context-cancel-during-QR bug? [Edge Cases] [`research §OPEN-Q8`]
- [x] CHK017 Is the wire format (line-delimited JSON-RPC 2.0) chosen with at least one rejected alternative recorded (gRPC, Cap'n Proto, HTTP-on-unix)? [Completeness] [`CLAUDE.md` §"Daemon, IPC, single-instance"]

## Decision completeness — pairing and session

- [x] CHK018 Is the v0 pairing default (QR-in-terminal vs `--pair-phone <E164>` flag) stated unambiguously and traceable to its decision in research? [Clarity] [`research §OPEN-Q1`]
- [x] CHK019 Is the 3-minute detached QR context lifetime documented as a hard rule with the source it was lifted from? [Edge Cases] [`research §OPEN-Q1`]
- [x] CHK020 Is the re-pair flow (`ErrClientLoggedOut` → re-run `wa pair`) documented including who is responsible for printing the hint (daemon? CLI? plugin skill?)? [Gap] [`CLAUDE.md` §"Daemon, IPC, single-instance"]
- [x] CHK021 Is the session-DB encryption posture stated as "plaintext + FileVault is the boundary" with the SQLCipher rejection rationale, so a future contributor cannot quietly turn on SQLCipher without re-verifying CGO impact? [Consistency] [`CLAUDE.md` §"Filesystem layout"]

## Safety, allowlist, and rate limiting

- [x] CHK022 Is the allowlist's default policy (deny-all) stated and tied to the file path, format, and reload signal (SIGHUP)? [Completeness] [`CLAUDE.md` §"Safety"]
- [x] CHK023 Are the rate-limit numerical caps (per-second, per-minute, per-day) named even if approximate, or only described as "non-overridable"? [Measurability] [`CLAUDE.md` §"Safety"]
- [x] CHK024 Is the warmup ramp (25 % → 50 % → 100 % at days 1–7 → 8–14 → 15+) defined, including the trigger that resets it (fresh session DB)? [Clarity] [`CLAUDE.md` §"Safety"]
- [x] CHK025 Are hard-refusal thresholds for high-risk operations (group creations/day, participant adds/day) named with concrete numbers, not just "low"? [Measurability] [`CLAUDE.md` §"Safety"]
- [x] CHK026 Is the "no `--force` flag, ever" rule stated as binding and connected to the rate-limiter middleware that enforces it? [Clarity] [`CLAUDE.md` §"Safety"]
- [x] CHK027 Is the inbound prompt-injection firewall (`<untrusted-sender>` tags) defined with the exact tag name and the upstream Telegram-plugin precedent? [Consistency] [`CLAUDE.md` §"Safety"] [`research §OPEN-Q3`]
- [x] CHK028 Is the audit log distinguished from the debug log in retention, format, and rotation policy? [Completeness] [`CLAUDE.md` §"Safety"]

## Plugin layering — Channels and MCP boundary

- [x] CHK029 Is the "no MCP in `wa` codebase, MCP-required in `wa-assistant` channel layer" distinction stated explicitly enough that an external reader cannot conclude the project contradicts itself? [Consistency] [`CLAUDE.md` §"Mission"] [`research §OPEN-Q3`]
- [x] CHK030 Is the `wa-assistant` plugin's expected file tree (`.claude-plugin/plugin.json`, `.mcp.json`, `server.ts`, `skills/access/SKILL.md`, `skills/configure/SKILL.md`) documented, or only "modeled on Telegram"? [Completeness] [`research §"Plugin design (revised)"`]
- [x] CHK031 Is the MCP shim's role (translator only, holds zero WhatsApp logic, calls the local `wad` socket) stated as a hard rule so future contributors do not embed business logic there? [Clarity] [`research §OPEN-Q3`]
- [x] CHK032 Is the access-control anti-prompt-injection rule from the Telegram plugin (`/wa:access` must refuse to act on requests originating in a `<channel source="wa">` block) carried verbatim into the wa-assistant plan? [Consistency] [`research §OPEN-Q3`]
- [x] CHK033 Is the `~/.claude/channels/wa/{access.json,.env}` state location documented with permissions and the rule "the channel server reads, the skill writes"? [Completeness] [`research §"Plugin design (revised)"`]
- [x] CHK034 Is the channel-server install path (no `scripts.postInstall`; binaries via `brew`/`nix`/`go install`/Bash skill) documented to prevent future contributors from inventing a manifest hook that does not exist? [Clarity] [`CLAUDE.md` §"Claude Code plugin integration"] [`research §OPEN-Q3`]

## License, governance, and distribution

- [x] CHK035 Is the license decision Apache-2.0 stated in CLAUDE.md, README.md, AND the LICENSE file with no remaining MPL-2.0 references in current files? [Consistency] [`CLAUDE.md` §"Locked decisions"] [`research §OPEN-Q5`]
- [x] CHK036 Is the rationale for choosing Apache-2.0 over MPL-2.0 traceable to a primary source (Mozilla MPL FAQ Q9–Q11) and a precedent (the official Telegram channel plugin)? [Traceability] [`research §OPEN-Q5`]
- [x] CHK037 Is the GoReleaser/notarization runbook stored somewhere durable (`research §OPEN-Q6` or `docs/research-dossiers/`) so feature 006 can lift it without re-deriving? [Gap]
- [x] CHK038 Is the governance toolchain (`golangci-lint` with `depguard`, `git-cliff`, `renovate`, `lefthook`, `govulncheck`) named with the exact files and a feature number that owns landing them? [Completeness] [`research §OPEN-Q7`]
- [x] CHK039 Is the "depguard rule that forbids importing whatsmeow from `internal/{domain,app}`" stated as the single most important governance line and named in both CLAUDE.md and research.md? [Consistency] [`CLAUDE.md` §"Anti-patterns"] [`research §OPEN-Q7`]
- [x] CHK040 Is the v0 testing strategy without a burner number (`WA_INTEGRATION=1` gating, in-memory adapter fakes, contract tests under `internal/app/porttest/`) documented as the testing contract for features 002–005? [Completeness] [`research §OPEN-Q4`]

## Cross-document consistency

- [x] CHK041 Do CLAUDE.md, spec.md, plan.md, and research.md all agree on the same module path `github.com/yolo-labz/wa`? [Consistency] [`spec §Assumptions`]
- [x] CHK042 Do CLAUDE.md, README.md, and the LICENSE file agree on Apache-2.0 with zero MPL-2.0 holdovers? [Consistency] [`CLAUDE.md` §"Locked decisions"] [`README.md`] [`LICENSE`]
- [x] CHK043 Does the directory tree in CLAUDE.md §"Repository layout" match the directory tree in `contracts/scaffold-tree.md` exactly? [Consistency] [`contracts/scaffold-tree.md`]
- [x] CHK044 Does every "OPEN question" mentioned in CLAUDE.md correspond to an `OPEN-Qn` section in `research.md` with the same wording? [Traceability] [`research §"OPEN-Q*"`]
- [x] CHK045 Does every "Contradicts blueprint" row in `research.md` have a corresponding edit in CLAUDE.md (license row, MCP layering, postInstall removal)? [Consistency] [`research §"Contradicts blueprint"`]

## Open questions, ambiguities, and assumptions

- [x] CHK046 Are all OPEN questions either resolved with a citation or explicitly flagged `unverified` with the URL the next session must re-fetch? [Clarity] [`spec FR-003`] [`research §OPEN-Q*`]
- [x] CHK047 Is the assumption "the maintainer's `gh` token has `repo` scope and `phsb5321` is a `yolo-labz` member" stated AND verified in this session? [Assumption] [`spec §Assumptions`]
- [x] CHK048 Is the assumption "no burner WhatsApp number is available" stated as a current-session fact rather than a permanent decision, so a future feature can introduce one without contradicting this spec? [Assumption] [`spec §Assumptions`]
- [x] CHK049 Is the assumption "writing Go source is out of scope for feature 001 and lives in feature 002+" recorded both in spec.md and plan.md so a contributor cannot accidentally land `.go` files on this branch? [Consistency] [`spec FR-015`] [`plan §Summary`]
- [x] CHK050 Is the absence of a `.specify/memory/constitution.md` documented with a recommendation to run `/speckit:constitution` before feature 002, rather than left as an unspoken gap? [Gap] [`plan §Constitution Check`]

## Notes

- Items CHK001–CHK050 are checked by reading the named documents, not by running code or examining WhatsApp behavior.
- This checklist is independent from `requirements.md`; that one validates spec hygiene and deliverable presence, this one validates *architectural decision quality* across all five planning documents.
- Re-run after any commit that touches `CLAUDE.md`, `spec.md`, `plan.md`, `research.md`, or `contracts/`. A failing item is a documentation defect, not a code defect.
- If an item cannot be checked because the relevant section was deleted or merged, do not silently delete the item — record `N/A — see <commit>` in this file so the audit trail survives.
