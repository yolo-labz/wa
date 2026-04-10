# Architecture & Modernity Checklist: Binaries and Composition Root

**Purpose**: Validate architectural decisions, tooling choices, and design coherence for feature 006.
**Created**: 2026-04-09
**Feature**: [spec.md](../spec.md), [plan.md](../plan.md), [research.md](../research.md)

## Composition Root Architecture

- [X] CHK001 Is the construction sequence specified in dependency order? [Completeness] [Data-model §Construction sequence] — Yes: 12-step sequence with each step's prerequisite built in a prior step.
- [X] CHK002 Is the shutdown sequence the exact reverse? [Consistency] [Data-model §Shutdown controller, FR-033] — Yes: socket → dispatcher → whatsmeow → sqlitehistory → sqlitestore → slogaudit → watcher.
- [X] CHK003 Is cmd/wad forbidden from containing use case logic? [Clarity] [FR-001] — Yes: "MUST NOT contain any use case logic beyond wiring."
- [X] CHK004 Is the dispatcherAdapter documented with enough detail? [Completeness] [FR-004, Data-model §dispatcherAdapter] — Yes: Go struct shown with Handle delegation + goroutine event conversion. ~20 LoC.
- [X] CHK005 Is the 10-second hard shutdown deadline documented? [Clarity] [FR-035] — Yes: "Total shutdown deadline is 10 seconds. If any adapter close hangs...logs an ERROR and exits non-zero."
- [X] CHK006 Is XDG directory creation specified for all 4 dirs? [Completeness] [FR-006] — Yes: data/config/state/runtime all listed, mode 0700, darwin fallback for runtime.

## Hexagonal Boundary Integrity

- [X] CHK007 Does the Pairer port maintain hexagonal invariant? [Consistency] [Research D1] — Yes: one-method interface in ports.go, memory fake implements it, composition root wires &adapter. No adapter imports from internal/app/.
- [X] CHK008 Is cmd/wa's import policy explicit? [Clarity] [FR-009] — **FIXED**: FR-009 now explicitly says "MUST NOT import internal/domain, internal/app, or any adapter package EXCEPT internal/adapters/primary/socket for socket.Path()."
- [X] CHK009 Is cmd/wad documented as the ONE place where layers meet? [Clarity] [FR-001, Plan §Constitution I] — Yes: plan constitution row I says "cmd/wad is the composition root — it's the ONLY place adapters and core meet."
- [X] CHK010 Is app-no-adapters depguard still enforced? New rules needed? [Gap] — The rule from feature 005 remains (verified in .golangci.yml). No new depguard rules needed for cmd/ — composition roots are permitted to import everything. Not a gap.

## CLI Design Quality

- [X] CHK011 Is every subcommand traceable to an FR and user story? [Completeness] — Yes: pair→FR-023/US1, send→FR-005/US1, allow→FR-016/US2, panic→FR-025/US3, status→FR-027/US4, groups→FR-028/US4, wait→FR-029/US4, sendMedia→FR-006/US1, react→FR-007/US1, markRead→FR-008/US1, version→FR-011/US6.
- [X] CHK012 Are global flags specified with defaults and override behavior? [Clarity] [FR-011, FR-012] — Yes: --socket defaults to socket.Path(), --json switches output format, --verbose increases log level. Override via flag.
- [X] CHK013 Is human vs JSON output specified for every subcommand? [Clarity] [FR-013, US6] — **FIXED**: FR-013 now includes explicit human format strings for send, status, groups, pair, allow list.
- [X] CHK014 Is the JSON schema string format documented for every method? [Completeness] [FR-013] — Yes: `wa.<method>/v1` format specified. US6 acceptance scenarios give concrete examples.
- [X] CHK015 Does exit code table cover every JSON-RPC error code? [Completeness] [FR-015, Contracts/exit-codes.md] — Yes: table covers -32011 through -32016, -32602, -32601, -32003, -32013, -32014, plus connection refused and generic fallback. All feature 004/005 codes accounted for.

## Allowlist Persistence

- [X] CHK016 Is atomic write-then-rename specified? [Clarity] [FR-017, Contracts/allowlist-toml.md] — Yes: "atomically persist...using a write-then-rename pattern" in FR-017; contract §Persistence rules says "Write to allowlist.toml.tmp, then os.Rename."
- [X] CHK017 Is fsnotify parent-dir watch + debounce + SIGHUP fallback documented? [Completeness] [FR-022, Research D3] — Yes: D3 specifies parent-directory watch + 100ms debounce; FR-022 says "Both mechanisms MUST work; the watcher is the primary path."
- [X] CHK018 Is malformed-TOML fallback specified? [Edge Cases] [FR-023] — Yes: "log an ERROR, keep the previous valid in-memory allowlist, and NOT crash."
- [X] CHK019 Is missing-file startup behavior specified? [Edge Cases] [FR-021] — Yes: "If the file does not exist, the daemon starts with an empty allowlist (default deny)."
- [X] CHK020 Is TOML schema documented with field types and example? [Completeness] [Contracts/allowlist-toml.md] — Yes: full example, field table with types, valid action values listed.
- [X] CHK021 Are allow add/remove audit entries specified and allow list exempt? [Completeness] [FR-024] — Yes: "allow add and allow remove MUST produce audit log entries (AuditGrant, AuditRevoke). allow list MUST NOT."

## Panic/Unlinking

- [X] CHK022 Is local-first semantics for panic specified? [Clarity] [FR-026] — Yes: "MUST always succeed locally even if the server-side unlink fails."
- [X] CHK023 Is post-panic state documented? [Completeness] [FR-028] — Yes: "session store cleared, in-memory 'not paired', subsequent send returns not-paired, pair starts fresh QR."
- [X] CHK024 Is AuditPanic entry with outcome variants documented? [Completeness] [FR-027] — Yes: "AuditPanic and outcome 'unlinked' or 'unlinked:local-only'."

## Tooling Modernity (Go 2026)

- [X] CHK025 Is charmbracelet/fang v2 confirmed? [Modernity] [Research D5] — Yes: D5 confirms actively released in 2025, provides styled help + --version + completion via single fang.Execute call.
- [X] CHK026 Is BurntSushi/toml confirmed? [Modernity] [Research D2] — Yes: D2 confirms de facto Go standard, zero transitive deps, not deprecated.
- [X] CHK027 Is fsnotify/fsnotify confirmed? [Modernity] [Research D3] — Yes: D3 confirms standard Go file watcher in 2026.
- [X] CHK028 Is testscript confirmed in go.mod? [Modernity] [Research D4] — Yes: D4 confirms v1.14.1 already in go.mod, actively maintained.
- [X] CHK029 Is slogaudit using stdlib slog? [Consistency] [Research D6] — Yes: D6 specifies slog.NewJSONHandler over os.File. No third-party logger.

## Signal Handling & Lifecycle

- [X] CHK030 Is signal.NotifyContext for SIGINT + SIGTERM specified? [Completeness] [FR-031] — Yes: exact function name and both signals listed.
- [X] CHK031 Is mid-pair SIGTERM behavior specified? [Edge Cases] [Spec §Edge Cases, US5 acceptance 5] — **FIXED**: US5 now has acceptance scenario 5 covering mid-pair QR flow cancellation under SIGTERM.
- [X] CHK032 Is post-shutdown socket cleanup + lock release verified by a test? [Completeness] [FR-007, US5] — Yes: US5 acceptance 1 says "socket file is unlinked" and US5 Independent Test says "asserts socket file is gone and lock released."

## Cross-Feature Consistency

- [X] CHK033 Do exit codes match CLAUDE.md? [Consistency] — Yes: CLAUDE.md says "0 ok, 64 usage, 10 not-paired, 11 not-allowlisted, 12 rate-limited, 78 config error" — contracts/exit-codes.md matches exactly.
- [X] CHK034 Does construction sequence reference correct type names? [Consistency] — Yes: data-model uses sqlitestore, sqlitehistory, slogaudit, whatsmeow, app.Dispatcher, socket.Server — all real symbols from features 003-005.
- [X] CHK035 Is socket.Path() the real symbol? [Consistency] — Yes: feature 004's lint fix renamed SocketPath→Path in path_darwin.go and path_linux.go. FR-012 references `socket.Path()`.
- [X] CHK036 Do allowlist TOML action strings match domain.Action.String()? [Consistency] — Verified: TOML uses "send", "read", "group.add", "group.create"; domain.Action.String() returns exactly those strings.

## Notes

- All 36 items pass. 3 items required spec fixes (CHK008, CHK013, CHK031).
- CHK008: FR-009 now explicitly enumerates cmd/wa's import policy with the socket.Path() exception.
- CHK013: FR-013 now includes human output format examples for the key subcommands.
- CHK031: US5 now has a 5th acceptance scenario covering mid-pair SIGTERM.
