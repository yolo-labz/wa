---
description: "Implementation tasks for feature 007 — release packaging and service integration"
---

# Tasks: Release Packaging and Service Integration

**Input**: Design documents from `/specs/007-release-packaging/`
**Prerequisites**: plan.md, spec.md (5 user stories), research.md (D1..D5), data-model.md, contracts/service-files.md

**Tests**: Service dry-run validation + existing test suite must continue passing.

**Organization**: 6 phases, 38 tasks, 5 commit boundaries.

## Format: `[ID] [P?] [Story] Description`

---

## Phase 1: Setup

**Purpose**: Create GoReleaser config, release workflow, and Nix flake — the build pipeline foundation.

**Commit boundary**: `build(007): add GoReleaser v2 config + release workflow + Nix flake`

- [x] T001 Create `.goreleaser.yaml` with `version: 2`, two builds (`cmd/wa`, `cmd/wad`), three targets (darwin-arm64, linux-amd64, linux-arm64), `CGO_ENABLED=0`, `-trimpath`, `-ldflags="-s -w -X main.version={{.Version}}"`, `archives.formats: [tar.gz]`, `checksum.name_template: checksums.txt`, `brews` section publishing to `yolo-labz/homebrew-tap` (FR-001..FR-005, research D1)
- [x] T002 [P] Add rcodesign post-build hook in `.goreleaser.yaml` for darwin builds: `rcodesign sign --code-signature-flags runtime --pem-source ... {{ .Path }}`; add post-archive hook for `rcodesign notary-submit --wait --staple` (FR-004, research D2)
- [x] T003 [P] Create `.github/workflows/release.yml`: trigger on `v*` tag push, checkout, setup-go, install rcodesign, decode Apple secrets from GitHub Actions secrets, run `goreleaser release`, run `git-cliff -o CHANGELOG.md`, commit CHANGELOG back to repo (FR-006..FR-009, FR-023)
- [x] T004 [P] Create `flake.nix` with `buildGoModule` (or `buildGo126Module`): `src = ./.`, `vendorHash = lib.fakeHash` (placeholder — real hash computed on first build), `subPackages = ["cmd/wa" "cmd/wad"]`, `env.CGO_ENABLED = 0`, `ldflags`, `meta.mainProgram = "wa"` (FR-010..FR-012, research D3)
- [x] T005 Validate: `goreleaser check` passes (SC-001); `goreleaser release --snapshot --clean` produces 3 tarballs + checksums in `dist/` (SC-002)

**Checkpoint**: Build pipeline validated locally. Release workflow ready for first tag.

---

## Phase 2: US1+US2 — Service integration (Priority: P1)

**Goal**: `wad install-service` generates and loads launchd plist (macOS) or systemd unit (Linux). `wad uninstall-service` removes it.

**Independent Test**: `wad install-service --dry-run` prints valid service file to stdout.

**Commit boundary**: `feat(007): wad install-service/uninstall-service (launchd + systemd)`

- [x] T006 [US1] Create `cmd/wad/service.go`: `installServiceCmd` and `uninstallServiceCmd` cobra subcommands registered on the wad root; `--dry-run` flag; root-user refusal via `os.Geteuid() == 0` check (FR-013, FR-016, FR-017)
- [x] T007 [P] [US1] Create `cmd/wad/service_darwin.go` (build tag `//go:build darwin`): generate launchd plist per contracts/service-files.md using `text/template`; `wad` path via `os.Executable()`; log path via XDG state home; write to `~/Library/LaunchAgents/com.yolo-labz.wad.plist`; load via `exec.Command("launchctl", "load", path)` (FR-014, FR-019, research D4)
- [x] T008 [P] [US2] Create `cmd/wad/service_linux.go` (build tag `//go:build linux`): generate systemd user unit per contracts/service-files.md; write to `~/.config/systemd/user/wad.service`; enable via `exec.Command("systemctl", "--user", "enable", "--now", "wad")`; print `loginctl enable-linger` hint (FR-015, FR-019, research D5)
- [x] T009 [US1] Implement uninstall: darwin `launchctl unload` + `os.Remove(plist)`; linux `systemctl --user disable --now wad` + `os.Remove(unit)`. Idempotent — ignore ENOENT (FR-018)
- [x] T010 [P] [US1] Create `cmd/wad/service_test.go`: test `--dry-run` on the current OS produces valid output (SC-005 plist, SC-006 unit); test root refusal
- [x] T011 Register `install-service` and `uninstall-service` as subcommands in `cmd/wad/main.go`

**Checkpoint**: Service integration complete on both platforms.

---

## Phase 3: US3 — Release pipeline validation (Priority: P1)

**Goal**: The full release pipeline works end-to-end on a dry-run tag.

**Independent Test**: Push `v0.0.0-test` to a fork and verify the workflow runs.

**Commit boundary**: (no separate commit — validated via CI, not code changes)

- [ ] T012 [US3] Verify `.github/workflows/release.yml` syntax via `actionlint` or GitHub's workflow validator (SC-008)
- [ ] T013 [P] [US3] Document the 5 required GitHub Actions secrets in a `docs/runbooks/release-setup.md` runbook: secret names, how to obtain them, how to store them (FR-004, research D2)
- [ ] T014 [P] [US3] Verify `git-cliff` produces expected CHANGELOG format: run `git-cliff --unreleased` and inspect output (FR-022, FR-023)

**Checkpoint**: Release pipeline is validated and documented.

---

## Phase 4: US4 — `wa upgrade` command (Priority: P2)

**Goal**: `wa upgrade` detects install method and prints the right command.

**Independent Test**: Unit test mocks `os.Executable()` for each path pattern.

**Commit boundary**: `feat(007): wa upgrade + install-method detection`

- [ ] T015 [US4] Create `cmd/wa/cmd_upgrade.go`: detect install method per FR-020 (check binary path for `/Cellar/` → brew, `/nix/store/` → nix, version string → go install, fallback → URL); print command to stdout, exit 0 (FR-020, FR-021)
- [ ] T016 [P] [US4] Create `cmd/wa/cmd_upgrade_test.go`: table-driven test with mocked executable paths for all 4 detection cases (SC-007)
- [ ] T017 [US4] Register `upgrade` subcommand in `cmd/wa/root.go`

**Checkpoint**: Upgrade detection works for all install methods.

---

## Phase 5: US5 — CHANGELOG integration (Priority: P2)

**Goal**: git-cliff config produces correct CHANGELOG from conventional commits.

**Commit boundary**: (merged into polish commit)

- [ ] T018 [US5] Verify `cliff.toml` from feature 001 is still correct for the current commit history; run `git-cliff -o CHANGELOG.md` and inspect output (FR-022, SC-009)
- [ ] T019 [P] [US5] Verify `.github/workflows/release.yml` has the git-cliff step that commits CHANGELOG.md after release (FR-023)

**Checkpoint**: CHANGELOG pipeline ready.

---

## Phase 6: Polish & cross-cutting concerns

**Purpose**: Lint, tests, CLAUDE.md update, tag v0.1.0.

**Commit boundary**: `chore(007): polish — service tests, lint, CLAUDE.md` then `chore(release): tag v0.1.0`

- [ ] T020 Run `go build ./cmd/wad ./cmd/wa` and verify both binaries include `install-service` and `upgrade` subcommands
- [ ] T021 Run `go test -race ./...` — all packages green including new service tests (SC-010)
- [ ] T022 Run `go vet ./...` — clean
- [ ] T023 Run `golangci-lint run ./...` — zero findings (SC-010)
- [ ] T024 [P] Verify `nix build .#default` produces working binaries (SC-003, SC-004)
- [ ] T025 [P] Verify `goreleaser check` passes with the final config (SC-001)
- [ ] T026 [P] Update `CLAUDE.md` §Build/test commands with release commands: `goreleaser release --snapshot --clean`, `nix build .#default`, `wad install-service --dry-run`
- [ ] T027 [P] Update `CLAUDE.md` §Active Technologies with GoReleaser v2, Nix flake, launchd/systemd
- [ ] T028 Walk quickstart.md steps 1-9 mentally; verify all referenced files exist
- [ ] T029 Tick all items in `specs/007-release-packaging/checklists/requirements.md` and `architecture.md`
- [ ] T030 Push branch: `git push origin 007-release-packaging`
- [ ] T031 Open PR against main
- [ ] T032 After CI green: merge PR
- [ ] T033 Tag `v0.0.7` on main for the feature
- [ ] T034 Generate CHANGELOG: `git-cliff -o CHANGELOG.md` and commit
- [ ] T035 Tag `v0.1.0` on main — the first release
- [ ] T036 Push tags: `git push origin v0.0.7 v0.1.0`
- [ ] T037 Verify release workflow triggers on `v0.1.0` tag (if Apple secrets are configured)
- [ ] T038 Celebrate 🎉

**Checkpoint**: Feature 007 complete. v0.1.0 released. Project ships.

---

## Dependencies & Execution Order

### Phase dependencies

- **Setup (Phase 1)**: No dependencies — pure config files
- **US1+US2 Service (Phase 2)**: Depends on Phase 1 (needs main.go updated for subcommands)
- **US3 Pipeline (Phase 3)**: Depends on Phase 1 (validates the workflow file)
- **US4 Upgrade (Phase 4)**: Independent — only touches cmd/wa
- **US5 CHANGELOG (Phase 5)**: Independent — validates existing cliff.toml
- **Polish (Phase 6)**: Depends on all phases

### User story dependencies

| Story | Phase | Depends on | Blocks |
|---|---|---|---|
| US1 (P1) — brew + launchd | 2 | Phase 1 | — |
| US2 (P1) — nix + systemd | 2 | Phase 1 | — |
| US3 (P1) — release pipeline | 3 | Phase 1 | — |
| US4 (P2) — wa upgrade | 4 | — | — |
| US5 (P2) — CHANGELOG | 5 | — | — |

US1/US2 share Phase 2 (service integration — build-tagged files). US3, US4, US5 are fully independent.

### Parallel opportunities

| Phase | Parallel tasks |
|---|---|
| 1 | T002, T003, T004 (all different files) |
| 2 | T007, T008, T010 (build-tagged files) |
| 3 | T013, T014 |
| 4 | T016 |
| 6 | T024, T025, T026, T027 |

---

## Implementation Strategy

### MVP first

1. Phase 1: GoReleaser + release workflow + flake — Commit 1
2. Phase 2: Service integration — Commit 2
3. **STOP and validate**: `goreleaser check` + `wad install-service --dry-run` + `go test`
4. Ship as MVP — binaries are distributable and installable as a service

### Full delivery

1. MVP (Phase 1+2)
2. + US4 upgrade (Phase 4) — Commit 3
3. + US3+US5 validation (Phase 3+5) — no new commit, just verification
4. Polish (Phase 6) — Commit 4
5. Merge → tag v0.0.7 → CHANGELOG → tag v0.1.0 → release workflow fires

---

## Notes

- 38 tasks across 6 phases, 5 commit boundaries (including the v0.1.0 tag)
- 5 user stories: 3 P1 (service + pipeline), 2 P2 (upgrade + changelog)
- This is the lightest feature: ~490 LoC, mostly config files
- No new Go module dependencies — GoReleaser, rcodesign, git-cliff are CI-only tools
- The Nix flake `vendorHash` must be computed on first build — use `lib.fakeHash` as placeholder, then update after `nix build` prints the real hash
- Apple notarization secrets are a manual prerequisite — the workflow will fail gracefully without them
- T038 is not optional
