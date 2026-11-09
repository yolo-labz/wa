# Tasks: Remove Ordinary-Message Daily Send Caps

**Input**: Design documents in `specs/113-no-daily-send-caps/`
**Prerequisites**: `plan.md`, `spec.md`, `research.md`, `data-model.md`, `quickstart.md`
**Tests**: Tests are mandatory and precede production-code changes because the removed limits are safety behavior.

## Phase 1: Red tests and preserved-safety baseline

- [ ] T001 [P] [US1] Add a held-out `testing/synctest` acceptance test in `internal/app/safety_test.go` that sends 31 ordinary messages to one known JID at two-second virtual intervals and proves every send is admitted; confirm it fails against the former per-recipient daily cap before changing production code.
- [ ] T002 [P] [US1] Add deterministic tests in `internal/app/ratelimiter_test.go` that admit 1,100 ordinary sends at two-second virtual intervals, which exceeds the former global 1,000/day bucket even after refill, and retain an immediate-third-send rejection test for burst 2; confirm the 1,100-send case fails before changing production code.
- [ ] T003 [P] [US2] Run and record the pre-change green baseline for warmup, burst, allowlist, audit, and idempotency coverage in `internal/app/ratelimiter_test.go` and `internal/app/safety_test.go`, plus group-create and participant-add daily-cap coverage in `internal/adapters/secondary/whatsmeow/groupadmin_test.go`.

## Phase 2: Minimal shared-path implementation

- [ ] T004 [US1] Remove the global-day, per-recipient-day, and unique-new-recipient-day state and checks from `internal/app/ratelimiter.go`, switch `internal/app/safety.go` to the remaining shared ordinary-send admission method, and preserve the 2/sec burst-2, 30/min burst-30, warmup, allowlist, audit, and idempotency behavior.
- [ ] T005 [P] [US1] Remove or rewrite benchmarks in `internal/app/ratelimiter_bench_test.go` that exercise the deleted per-recipient daily-cap API while preserving useful limiter benchmarks.
- [ ] T006 [US1] Delete the now-unused `KnownRecipientFunc`/`SetKnownRecipientFunc` plumbing and composition callback from `internal/app/dispatcher.go` and `cmd/wad/main.go`, and correct the stale daily-limit comment in `cmd/wad/history_methods.go`.
- [ ] T007 [US2] Run the targeted post-change tests in `internal/app/ratelimiter_test.go`, `internal/app/safety_test.go`, and `internal/adapters/secondary/whatsmeow/groupadmin_test.go`, then use `git grep` over `internal/` and `cmd/` to prove the three ordinary-message daily caps and known-recipient limiter callback have no remaining production callers.

## Phase 3: Living documentation and generated copies

- [ ] T008 [P] [US3] Replace ordinary-message daily-cap claims with the exact 2/sec burst-2 and 30/min burst-30 policy in `README.md` and `README.pt-BR.md`, preserving the separate group-admin caps, no-broadcast rule, and no force/bypass behavior.
- [ ] T009 [P] [US3] Update the same policy and rollback/canary guidance in `SECURITY.md`, `docs/manual.md`, and `docs/roadmap.md` without rewriting historical incident evidence.
- [ ] T010 [P] [US3] Update the canonical agent text in `internal/agentdocs/llms.txt` and its generated root mirror `llms.txt`, then run the mirror assertion in `internal/agentdocs/agentdocs_test.go`.
- [ ] T011 [P] [US3] Update the embedded copy in `packaging/mcpb/manifest.template.json`, run `scripts/build-mcpb.sh`, and validate the resulting JSON so the generated MCPB description contains no ordinary-message daily-cap claim.
- [ ] T012 [P] [US3] Update `.github/og-card.html` and regenerate `.github/og-card.png`, preserving the expected image dimensions while removing ordinary-message daily-cap marketing text.
- [ ] T013 [P] [US3] Update `.github/demo.cast` to demonstrate the revised ordinary-send policy and validate every JSON event line remains parseable.
- [ ] T014 [P] [US3] Mark the superseded ordinary-message daily-cap recommendations in `issues.md` and `research.md` as historical, while leaving their incident record intact and pointing to `specs/113-no-daily-send-caps/constitution-amendment.md` as the current decision.
- [ ] T015 [US3] Run a repository-wide living-document scan from `/home/notroot/Documents/Code/yolo-labz/wa-358-no-daily-send-caps` and prove no source, public, agent, or generated copy still presents a global, per-recipient, or unique-new-recipient daily cap as current ordinary-send behavior; allow only explicit historical/superseded references and the separate group-admin caps.

## Phase 4: Quality gates

- [ ] T016 [US2] Run `gofumpt` on changed Go files and rerun the targeted acceptance and preserved-safety tests in `internal/app/ratelimiter_test.go`, `internal/app/safety_test.go`, and `internal/adapters/secondary/whatsmeow/groupadmin_test.go`.
- [ ] T017 [P] [US2] Run the full race suite `go test -race -shuffle=on -count=1 ./...` from `/home/notroot/Documents/Code/yolo-labz/wa-358-no-daily-send-caps` and retain the green receipt.
- [ ] T018 [P] [US2] Run `golangci-lint run` from `/home/notroot/Documents/Code/yolo-labz/wa-358-no-daily-send-caps` and retain the green receipt.
- [ ] T019 [P] [US2] Run `go build ./...` from `/home/notroot/Documents/Code/yolo-labz/wa-358-no-daily-send-caps` and retain the green receipt.
- [ ] T020 [US2] Run `nix flake check --no-build` followed by the repository Nix build targets documented in `specs/113-no-daily-send-caps/quickstart.md`, using all available cores, and retain the green receipts.

## Phase 5: Review, merge, canary, and controlled recovery

- [ ] T021 [US4] Prepare the conventional-commit PR from `/home/notroot/Documents/Code/yolo-labz/wa-358-no-daily-send-caps`, with honest ✓/◐/◯ evidence plus the canary and one-revert-PR rollback plan from `specs/113-no-daily-send-caps/quickstart.md`; push only the feature branch and open the PR without deploying.
- [ ] T022 [US4] Submit the exact PR head SHA to a capable full `zai/glm-5.3:max` different-family review, repair every critical/high finding in `/home/notroot/Documents/Code/yolo-labz/wa-358-no-daily-send-caps`, repeat review after each head change, and retain the final exact-head verdict together with green required checks and resolved threads.
- [ ] T023 [US4] Stop `wa-deploy-sync.timer` and `cafezinho-replay-after-rate-reset.timer` before merge, verify both are inactive, squash-merge the review-clean green PR without bypass, and record the merge SHA/state receipt alongside `specs/113-no-daily-send-caps/quickstart.md` evidence.
- [ ] T024 [US4] Following `specs/113-no-daily-send-caps/quickstart.md`, manually deploy and verify `wa-burocracy` first using app-level inbound/event progress in addition to transport state, then deploy and verify `wa-personal`; if rollback is needed, create one revert PR and deploy the revert to bureaucracy before personal.
- [ ] T025 [US4] Using `/run/cafezinho-replay-after-reset.sh` only after both canaries pass, assert there are exactly four pending Cafezinho deliveries, select the unique maximum `updatedAt`, replay exactly that delivery, verify status `delivered` plus a non-fallback reply, then prove `cafezinho-replay-after-rate-reset.timer` is absent/cancelled and `/run/cafezinho-replay-after-reset.sh` is absent/removed before restoring `wa-deploy-sync.timer`.

## Dependencies and execution order

- T001–T003 must finish before T004; their pre-change results distinguish a real behavior change from a speculative edit.
- T004 blocks T005–T007. T007 blocks the documentation and quality phases.
- T008–T014 are parallel after T007; T015 consolidates their result.
- T016 and T015 block T017–T020. T020 blocks T021.
- T021 → T022 → T023 → T024 → T025 is strictly sequential. No deploy, replay, or message send occurs during specification authoring.

## Acceptance mapping

| Requirement | Tasks |
|---|---|
| More than 30 paced sends to one known JID pass | T001, T016, T017 |
| Former global 1,000/day bucket is deterministically falsified | T002, T016, T017 |
| Immediate third send still rejects | T002, T016, T017 |
| Group-admin daily caps remain green | T003, T007, T016, T017 |
| Dead known-recipient limiter callback is removed | T006, T007 |
| All current source/public/agent/generated claims agree | T008–T015 |
| Full Go race, lint, build, and Nix gates pass | T017–T020 |
| Exact-head different-family review and merge receipt | T022, T023 |
| Timer stop, bureaucracy-first app-liveness canary, then personal | T023, T024 |
| Exactly one newest-of-four Cafezinho recovery succeeds and cleanup is proven | T025 |
| One-revert-PR rollback deploys bureaucracy first | T021, T024 |
