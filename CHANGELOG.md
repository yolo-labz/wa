# Changelog

All notable changes to this project are documented here.
## [unreleased]


### Bug Fixes
- Match --caption without regard to case or accents (#322)
- Report the real pairing instant as sessionSince, or nothing (#324)
- Refuse pairing on a wiped store instead of faking success (#323)
- Refuse media.list rows a daemon did not confirm filtering (#320)
- Stop collapsing RPC sentinels to -32603 over --remote (#312)
- Classify catalogued daemon error codes into exit buckets (#306)
- Surface the PTT flag on inbound audio in media.list (#308)
- Guard the reverse of the openrpc drift check (#307)
- Wa_search_messages targeted the wrong RPC method (#305)
- Replay buffered events on subscribe --since (FR-061) (#304)
- Persist reactions instead of blank rows (#301)
- Stamp a monotonic seq on every event (#300)
- Map param errors to -32602, not -32603 (#299)
- Persist stickers and pins, keep history variants (#298)
- Deliver event payload and FR-060 filter selectors (#297)
- Route every domain event variant, fail closed on the rest (#296)
- Read message text through Content, not a type switch (#295)
- Project every message variant, not just three (#294)
- Harden mention validation per adversarial review (#293)
- Bump Go 1.26.4->1.26.5 for reachable GO-2026-5856/-4970 (#287)

### Chore
- Apply go 1.26.5 go-fix modernizations + gate-blocking clone dedup (#327)
- Gitignore the coverage artifacts our own docs tell you to make (#321)
- Drop stale Go patch version from .golangci.yml comments (#291)
- Bump whatsmeow to 20260716 + x/net,sys,text train (#289)

### Documentation
- Fix --caption example that matches nothing (#316)
- Correct --since semantics after seq stamping landed (#303)
- Regenerate stale v2.x history via git-cliff (#290)
- Document the shipped MCP server + sync stale metadata (#288)

### Features
- Narrow media list by sender, caption and time window (#314)
- Add played receipts for voice notes and view-once (#309)
- SendMedia --ptt sends audio as a voice note (#302)
- Add @mention support to wa send (#292)

### Refactor
- Collapse duplicated write and handler prologues (#313)

### Tests
- Pin Standard Webhooks 1.0 delivery headers (huly WA-3) (#329)
- Streamable-HTTP conformance round-trip (spec 111 M2) (#328)
## [2.2.0] - 2026-07-02


### Bug Fixes
- Surface contact vCards on the live inbound path too (#283)
- Surface contactMessage vCard in history sync (#281) (#282)
- Complete channel firewall on contacts/groups/transcript reads (#279)
- Wrap inbound stored messages in the channel firewall (#278)
- Bump Go to 1.26.4 to clear 2 reachable stdlib CVEs (#277)
- Resolve MIME + filename so documents arrive openable (#268)
- Close-error hygiene on write-capable handles (SF-11..14) (#266)
- Back off + surface eventbridge stream errors (SF-08) (#260)
- Actively drop poison rows at embed retry bound (SF-07) (#259)
- Audit allowlist reload outcomes from every trigger (SF-05) (#258)
- Keep migration marker on incomplete rename rollback (SF-04) (#257)
- Abort migration pre-flight on unreadable probes (SF-10) (#256)
- Add correlation ref to dispatcher panic path (SF-09) (#255)
- Surface silent failures from 018 audit (SF-01..03) (#246)
- Path-hygiene sweep — SEC-05/07/08/09 from 018 audit (#244)
- Record stream drops in resume ring unconditionally (#242)
- Unblock fan-out, join fires, scope seq counter (#241)
- Cancel daemon-lifetime callbacks + join retention on shutdown (#240)

### Chore
- Bump whatsmeow to 2026-06-11 head (62 commits) (#265)
- Close stale 018 rows + enforce DCO in commit-msg hook (#252)

### Documentation
- Fix PR-number provenance in vCard comments (#282/#283) (#284)
- Fix SECURITY.md threat-model drift (T1 tag, T7 export) (#276)
- Tick REL-03 stale (pie shipped in #170), refresh REL-14 bullet (#273)
- Add Brazilian Portuguese quickstart (README.pt-BR.md) (#264)
- Refresh release-engineering claims to v2.1.0 reality (#263)
- Amend constitution to v1.1.0 (scope whatsmeow test-import rule) (#247)

### Features
- Resources + prompts (spec 111 M3) (#285)
- Agentic eval set + tool snapshot in CI (FR-111-08) (#286)
- Opt-in offline presence on connect (WA_PRESENCE_OFFLINE) (#280)
- SendSeen — mark chat read at newest incoming message (#271)
- Wire durable ring + SSE Last-Event-ID replay (ARCH-01) (#269)
- --humanize typing presence + jittered delay (roadmap 2.3) (#243)
- .mcpb bundles + MCP Registry server.json (roadmap 0.1) (#239)
- RFC 9457 problem+json for REST-layer errors (feature 113) (#237)

### Refactor
- Single-source pair-page HTML in internal/pairhtml (#272)
- Remove orphan EventBus/EventSubscription ports (ARCH-01) (#270)
- Split server.go and adapter.go below 500 lines (ARCH-04) (#267)
- Fold ProtoVersion into domain/schema.go (ARCH-05) (#262)
- Single-source pair-HTML path, wire FR-014 profile suffix (#261)

### Tests
- De-flake Close-joins-panic-goroutine assertion (#274)
- Add FuzzCanonicalJSON + FuzzFrameRecv fuzz targets (TEST-08) (#254)
- Cover handlePanic always-success contract (TEST-17) (#253)
- Assert deny-path audit decisions in send pipeline (TEST-07) (#251)
- Export AL/CD/GM/SS standalone contract runners (TEST-04) (#250)
- E2e-cover 15 undriven wa subcommands (TEST-10) (#249)
- Export MS/ES/HS standalone contract runners (#248)
- Tier-2 dispatcher contract tests (TEST-06) (#238)

### Ci
- Add quality-gates (code-slop + alignment) on the PR diff (#275)
- Build, attest + publish .mcpb bundles to MCP Registry (#245)
## [2.1.0] - 2026-06-10


### Bug Fixes
- Close SEC-03/04/06 from the 018 audit (#234)
- Wrap subscriber event payloads in channel envelope (#227)
- Raise soft-stale threshold default to 900s (anti-flap) (#223)
- Anchor on-demand sync, stop nil-deref daemon crash (#222)
- Enforce 0700 socket parent, drop process-global umask (#220)
- Log offline-sync count for soft-stale diagnostics (#217)
- Capture stack trace on dispatcher panic recovery (#214)
- Add _txlock=immediate to session.db DSN (#211)
- Self-stamp version from git when build args absent (#208)
- Don't follow redirects on --remote calls (#198) (#204)
- Give go test a short writable TMPDIR on all runners (#205)
- Prune migration backups on every startup (#202)
- Enforce 1 MiB frame cap, return -32004 (DoS) (#196)
- Broaden error-code exit mapping + actionable hints (#189)
- Transcribe voice notes that sniff as application/ogg (#185)
- Repair messages_au FTS trigger so msg edit works (#184)
- Wire searchable mirror so search/sync work (#183)
- Contact lid/pn, export-miss exit, live profile probe (#182)
- Populate ContextInfo.RemoteJID on reply-class sends (#165) (#166)
- Hydrate ContextInfo.QuotedMessage on reply-class sends (#163) (#164)
- Quote inbound interactive via ContextInfo on reply (#161) (#162)
- Map send.{button,list}Response + session.logout (#160)
- Pin distroless runtime by sha256 (#159)
- Bump Go 1.26.3 + x/net 0.54 + pin docker image (#156)

### Chore
- Add -buildmode=pie to GoReleaser builds (#170)

### Documentation
- Feature 111 - MCP primary adapter design (#228)
- Market-leadership roadmap (MCP-first, evidence-based) (#226)
- Vendor yolo-labz release-engineering standards into CLAUDE.md (#225)
- Adopt merge-ownership rule (done = merged) (#216)
- Encode daemon liveness/watchdog/diagnosability rules (#215)
- Cover all 44 CLI commands in manual §6 + fix Go version (#193)
- Cloud-API peers workaround taxonomy (closes #167 Action 2) (#168)

### Features
- Reproducible benchmarks + idle-RSS script (roadmap 2.2) (#236)
- OpenAPI + OpenRPC contracts and Scalar /docs (roadmap 1.2) (#235)
- Checksum-verified install.sh + compose quickstart (#233)
- Standard-webhooks outbound delivery (feature 112) (#232)
- Agent-readable surface - llms.txt + error catalog (111/0.2) (#231)
- Streamable HTTP transport with scope-filtered tools (111 M2) (#230)
- Stdio MCP server with draft-gated sends (spec 111 M1) (#229)
- Exponential reconnect backoff for quiet-stretch soft-stale (#224)
- Backfill missed messages after soft-stale recovery (#221)
- Persist delivery/read receipts for wa thread (#219)
- Reject sends to non-WhatsApp numbers (pre-send gate) (#218)
- Opt-in soft-stale auto-reconnect (WA_SOFT_STALE_RECOVER) (#212)
- Add sendMedia --sha256 to send already-staged media (#209) (#210)
- Decode + persist inbound interactive replies (#201) (#206)
- Add socket media.fetchBytes for download parity (#203)
- Remote upload (POST /media/upload, wa push, --remote) (#199)
- SendMedia accepts inline bytes (remote-upload seam) (#197)
- Accept --chat as universal recipient alias (#195)
- Wa privacy get/set + wa poll vote commands (#191)
- Wa sync force/status + wa stream for history visibility (#188)
- File-management discovery commands + export filters (#187)
- GET /media/{sha256} fetch endpoint + wa media fetch (#186)
- Hero SVG + capability + comparison + asciinema + OG (#172)

### Refactor
- Extract buildPorts from run() (#194)

### Tests
- Prove soft-stale auto-recovery end-to-end (#213)
- Cover graceful-shutdown chain + allowlist hot-reload (#190)

### Ci
- Harden-runner on all workflows + document gosec suppressions (#192)
## [2.0.15] - 2026-05-19


### Bug Fixes
- Install git in builder for -buildvcs=true (#155)
- Grant security-events:write to gitleaks workflow (#154)
- Close waits for LoggedOut panic goroutine (#136)
- Cap messages.db WAL via journal pragmas (#137)
- Update vendorHash for new Go deps (--remote HTTP transport)
- Bump base to golang:1.26.2-alpine3.22 (1.26.1 / alpine3.20 EOL on dockerhub) (#135)
- Typed errors for media.download (#102) (#103)
- Restore depguard hexagonal boundary post /v2 migration (#100)

### Chore
- Wrapcheck config drafted, linter held disabled (T059) (#131)
- Linter expansion — sqlclosecheck + perfsprint + fatcontext (T056-T058/T062/T063) (#128)
- Finalize verification suite (T060/T061/T083/T085-T088) (#126)
- Coverage CI gate + Dispatcher pattern doc + verify (T072/T073/T082/T084) (#125)
- FTS5 baseline + verify T075/T076 done (T046/T075/T076) (#124)
- Tamper-evident audit log via HMAC chain (#122)
- Audit Source/event_time + CI go mod verify (T051/T052/T071) (#121)
- Close US5 LogValuer suite (T068/T069/T070) (#120)
- US5 bundle — fuzz corpus + rate-limit bench + JID LogValuer (T065/T066/T067) (#119)
- Close US2 — cmd/wa exit-code + error-wrap normalization (T033-T038) (#118)
- Rows.Close logging + launchd PATH override + verify fuzz target (#117)
- SQLite hardening + magic-number cleanup (T044/T045/T048-T050) (#116)

### Documentation
- Add services footer — spec 020 FR-006 (#146)
- Point port count at canonical registry (#138)
- Bump Go pin 1.26.1 to 1.26.2 to match go.mod (#139)
- Refresh quickstart + verify pragmas + walk it (T089/T090/T091) (#127)
- Note /v2 import path on go install bootstrap (#104)

### Features
- Outbound list/button reply (send.listResponse, send.buttonResponse) (#153)
- Wa --version + NixOS system-profile hint + stale footer (110i) (#152)
- Wire media.download --transcribe end-to-end (110h) (#151)
- Soft-stale detection + synthetic state events (110g) (#149)
- Wa pair --reset non-destructive re-pair (110f) (#148)
- Wa pair --remote <host>:<app> SSH-chain UX (110e) (#147)
- Add wa.heap.bytes OTel gauge for leak detection (#143)
- Sqlite tokens store + wad token admin (spec 110d) (#115)
- SSE events bridge — GET /v1/events (spec 110b v0) (#114)
- Wa --remote URL HTTPS transport (spec 110c v0) (#113)
- REST primary adapter skeleton (spec 110a) (#112)
- Dokku container + SSH-forward multi-host CLI (spec 109) (#109)
- Expand JID parser — hosted, bot, channel, broadcast refusal (#108)
- Preserve PN/LID addressing mode in messages.db (#107)
- IdentityResolver port for PN <-> LID translation (#106)
- First-class LID JID support (@lid server) (#105)

### Performance
- Mmap_size=0 — security AND perf win (T047) (#130)

### Refactor
- Graceful shutdown on startupCleanup + drop gocyclo nolint (T080/T081/T079-deferred) (#134)
- Extract initRuntime from cmd/wad/main.go (T077) (#133)
- Extract openStores from cmd/wad/main.go (T078) (#132)
- Centralise startup cleanup into startupCleanup (T020) (#129)
- Allowlist debouncer + rate-limiter helper (T021/T074) (#123)

### Ci
- Pin Go via go-version-file: go.mod across all workflows (#141)
- Unblock — gocyclo nolint + bump golangci-lint v2.11.4 (#142)
- Sanitize inherited JAVA_TOOL_OPTIONS so sonar-scanner starts (#140)
- Migrate to self-hosted runner (#101)
## [2.0.14] - 2026-04-28


### Bug Fixes
- Version reports module version for go-install builds (#99)
## [2.0.13] - 2026-04-28


### Bug Fixes
- Bump TestOpenSecondFailsWithLockContention timeout 150ms→500ms (#97)

### Chore
- Migrate module path to github.com/yolo-labz/wa/v2 (#98) [**breaking**]

### Build
- Add lint-cross-platform target (#96)
## [2.0.12] - 2026-04-28


### Performance
- Refresh PGO profile after lint sweep + ctx threading (#93)

### Ci
- Tparallel + 4 free-coverage linters; strip 38 unused nolints (#95)
- Enable usestdlibvars + intrange (#94)
## [2.0.11] - 2026-04-27


### Ci
- Re-enable noctx + thread ctx through 11 sites (#92)
- Enable gocheckcompilerdirectives (#91)
## [2.0.10] - 2026-04-27


### Bug Fixes
- No-ai-slips shellcheck SC2001 (sed → bash param expansion) (#85)
- Sync no-ai-slips workflow with portfolio#14 self-match fix (#84)
- Scrub recruiter-pipelines mention from README cross-link (#82)

### Documentation
- Add OpenSSF Scorecard, SLSA L2, Sigstore badges (#79)

### Ci
- Enable unconvert + noctx (#90)
- Enable bodyclose (#89)
- Enable nilnil (#88)
- Enable predeclared + dupword (#87)
- Enable wastedassign + misspell (#86)
- Enable sloglint for structured-logging discipline (#81)
- Add no-ai-slips lint to catch stealth + framing leaks pre-merge (#83)
- Bump golangci-lint go-version + enable errorlint (#80)
## [2.0.9] - 2026-04-27


### Bug Fixes
- Bump filippo.io/edwards25519 v1.1.0 → v1.2.0 (#78)

### Chore
- Bump go directive 1.26.1 → 1.26.2 (#77)
## [2.0.8] - 2026-04-27


### Bug Fixes
- Include openvex.json in checksums.txt for attestation coverage (#76)
## [2.0.7] - 2026-04-27


### Ci
- Publish OpenVEX 0.2 statements alongside release artefacts (#75)
## [2.0.6] - 2026-04-27


### Chore
- Guard whatsmeow PR against PR #955 regression (#74)

### Documentation
- Bump VERSION to v2.0.5 + verify the tarball (#72)

### Performance
- Wire real PGO capture + commit baseline default.pgo (#73)
## [2.0.5] - 2026-04-27


### Bug Fixes
- Attestations cover real artifacts (subject-checksums) (#71)

### Ci
- Flip enforce — cleanup passes complete (#70)
- Trailing-comment permissions + name 4 anonymous jobs (#69)
- Flip enforce + tighten suppressions (artipacked closed) (#68)
- Persist-credentials:false on all remaining checkout sites (#67)
## [2.0.4] - 2026-04-27


### Chore
- Add .osv-scanner.toml config (#59)
- Bump goreleaser pin v2.5.1 → v2.6.1 (#58)

### Documentation
- Rekor v2 verify note + bleeding-edge gaps tracker (#57)

### Features
- Ci-local + bench-canonical + pgo-capture targets (#56)

### Ci
- Add goreleaser-check fast gate (workflow + Makefile + lefthook) (#66)
- Permissions rationale on all 3 release.yml blocks (#65)
- Rationale comments across 7 remaining workflows (#64)
- Add rationale comments to ci.yml job blocks (#63)
- Permissions rationale + named job + persist-credentials (#62)
- Cache:false on every self-hosted setup-go (#61)
- Add .github/zizmor.yml documented suppressions (#60)
## [2.0.3] - 2026-04-26


### Chore
- Add install-dev-tools.sh, CodeQL config, SECURITY.md enrichment (#55)
- Pin syft installer to v1.43.0 with sha256 verify (#52)
- Wire SonarQube scan workflow (yolo-labz_wa) (#46)
- Bump attest-build-provenance + attest-sbom v2.4.0 → v4.1.0 (#49)

### Documentation
- Refresh install + supply-chain + roadmap to current state (#50)

### Features
- Add nfpms block for .deb/.rpm/.apk Linux packages (#51)
- Bench canonicaljson + gitleaks pre-push and GHA (#48)

### Performance
- CanonicalJSON typed fast-path — 3x speedup (#54)

### Ci
- Add actionlint + zizmor workflow static analysis (#53)
- Move osv-scan + reproducibility to self-hosted dokku runners (#47)
## [2.0.2] - 2026-04-26


### Bug Fixes
- Send system.hello handshake before user method (#42)

### Chore
- Add SonarQube scan workflow (yolo-labz_wa) (#43)

### Documentation
- Add yolo-labz ecosystem footer (#40)

### Features
- Local SonarQube via docker compose + opt-in pre-push (#45)

### Refactor
- Consolidate to ci.yml + fix exclusions (#44)
## [2.0.1] - 2026-04-23


### Bug Fixes
- Soft-warn Apple decode step on GA (v2.0.1 fix-forward) (#39)
- FR-053 — Apple signing conditional, loud warning on unsigned darwin (#38)
- Install git-cliff as binary, not via pip (#37)
- Cyclonedx-gomod expects module root + -main flag (#36)
- Use CycloneDX 1.6 (syft lacks 1.7 support) (#35)
- Disable gomod.proxy for v2+ tags (#34)

### Features
- Tier 3 observability + fuzz + release hardening (PR-3) (#33)
- Tier 2 parity hardening — 7 new ports + 20 P0 methods (PR-2) (#32)
- Tier 1 parity hardening (feature 018) (#31)
## [1.2.1] - 2026-04-21


### Bug Fixes
- Wire MediaStore + feature-017 ports in wad
- Target Formula/ directory for homebrew tap publication (#28)

### Chore
- Regenerate CHANGELOG.md for v1.2.1
## [1.2.0] - 2026-04-21


### Bug Fixes
- Dereference tag-object SHAs to commit SHAs (scorecard imposter) (#26)
- Round-3 main-workflow repair (scorecard commit SHA, repro buildvcs) (#25)
- Round-2 main-workflow repair (homebrew skip, scorecard env) (#24)
- Repair post-merge main workflows (Reproducibility, Scorecard) (#23)

### Chore
- Regenerate CHANGELOG.md for v1.2.0 (#27)

### Features
- Agent experience three-tier release (feature 017) (#22)
## [1.1.0] - 2026-04-13


### Bug Fixes
- Wrap []byte params as json.RawMessage to prevent base64 encoding (#21)
- Enable history sync by setting ManualHistorySyncDownload=false (#20)

### Chore
- Update CHANGELOG.md for v1.1.0

### Features
- History sync and message persistence (feature 009) (#18)

### Refactor
- Code quality audit & modernization (feature 016) (#19)
## [1.0.1] - 2026-04-13


### Bug Fixes
- Enable WAL mode + busy_timeout on session.db (#17)
- GOTOOLCHAIN=local so nix build accepts nixpkgs Go 1.26.1
## [1.0.0] - 2026-04-12


### Features
- Wa — WhatsApp automation CLI + daemon

