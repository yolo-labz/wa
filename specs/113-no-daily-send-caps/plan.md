# Feature 113 — implementation plan: no daily ordinary-send caps

**Branch**: `358-no-daily-send-caps`  
**Specification**: [spec.md](./spec.md)  
**Constitution**: [v2.0.0 amendment](./constitution-amendment.md)  
**Plan status**: ready for task generation

## Summary

Delete ordinary-send calendar-day state at the shared safety choke point, keep
the two short token buckets and warmup, remove dead recipient-history wiring,
add deterministic tests that fail the former mechanisms, synchronize every
living safety claim, and release through a bureaucracy-first manual canary.

No new abstraction, dependency, port, RPC, wire field, configuration option,
or persisted migration is needed.

## Technical context

| Area | Decision/evidence |
|---|---|
| Language/toolchain | Go 1.26.5 (`go.mod:1-4`) |
| Limiter primitive | Existing `golang.org/x/time/rate`; retain two buckets (`internal/app/ratelimiter.go:8`, `internal/app/ratelimiter.go:15-18`) |
| Deterministic time tests | `testing/synctest`, already used with this limiter (`internal/app/ratelimiter_test.go:211-236`) |
| Concurrency | Existing token buckets are shared across send paths; delete recipient map/mutex state and retain race-suite coverage |
| Storage | No new or changed persisted state; see [data-model.md](./data-model.md) |
| Public contract | Behavior changes, no wire-schema or error-code change |
| Build | Native `go build ./cmd/wa ./cmd/wad`; Nix builds both binaries (`flake.nix:57-65`, `flake.nix:144-149`) |
| CI parity | Race/shuffle/no-cache command at `.github/workflows/ci.yml:210-221`; Nix commands at `.github/workflows/ci.yml:385-394` |

## Constitution check — pre-design

| Gate | Result | Evidence |
|---|---|---|
| Speckit order and ≤25 tasks | PASS | Constitution decision → specify → zero-question clarify scan → plan; tasks are generated next |
| WHAT, finite checks, examples + properties | PASS | `spec.md` has measurable requirements and 13 Given/When/Then scenarios paired with 13 universal properties |
| Read/cite before write | PASS | Every planned code claim cites current `file:line`; all target files were read or confirmed absent |
| Minimal shared fix | PASS | One shared limiter and one dead callback chain cover every send path (`internal/app/safety.go:20-27`, `cmd/wad/main.go:439-456`) |
| Amended Safety V.26 | PASS | Only daily ordinary-send accounting is removed; short windows, warmup, group caps, broadcast refusal, and no bypass remain |
| Concurrency gate | PASS | Time tests use `synctest`; full race/shuffle suite is mandatory; no new goroutine means no new goleak obligation |
| Diagnosable/non-speculative | PASS | Concrete fields and reproduced first decisive error are named in `research.md`; status is verified root cause, not analogy |
| Deploy safety | PASS | Rollback and bureaucracy-first app-liveness canary are mandatory before personal |

## Exact implementation surfaces

### Tests first

| Path | Planned change |
|---|---|
| `internal/app/safety_test.go` | Add the held-out 31-send same-JID pipeline test on a virtual clock; preserve the immediate-third, allowlist-order, warmup, and happy-path tests. |
| `internal/app/ratelimiter_test.go` | Add the 1,100-send former-global-bucket falsifier; keep or strengthen the immediate-third assertion and existing warmup/refill coverage. |
| `internal/adapters/secondary/whatsmeow/groupadmin_test.go` | No edit expected; run the named 5/day and 50/day tests as regression gates. |

### Minimum production deletion

| Path | Planned change |
|---|---|
| `internal/app/ratelimiter.go` | Remove global day constants/bucket, recipient daily constants/state/reset logic, known-recipient type/callback, and recipient-specific methods. Keep two short buckets, warmup helpers/constructors, `Allow`, `Warmup`, and denial type. |
| `internal/app/safety.go` | Preserve allowlist-first ordering, then call recipient-independent `Allow`. |
| `internal/app/ratelimiter_bench_test.go` | Remove the dead per-recipient/map benchmark; retain the ordinary hot-path benchmark. |
| `internal/app/dispatcher.go` | Remove `SetKnownRecipientFunc`. |
| `cmd/wad/main.go` | Remove the sole history-backed known-recipient callback; leave shared SafetyPipeline wiring untouched. |
| `cmd/wad/history_methods.go` | Remove/update the stale comment that cites `SetKnownRecipientFunc`; no behavior change. |

### Living documentation and generated copies

| Paths | Planned change |
|---|---|
| `README.md`, `README.pt-BR.md` | Replace 2/30/1000 and incorrect 2/min·30/hour claims with exact short-window rates/bursts and no daily ordinary-send cap. |
| `SECURITY.md` | Replace per-day-volume mitigation with short-window loop containment plus preserved independent safeguards. |
| `docs/manual.md`, `docs/roadmap.md` | Correct error/limit tables and product-positioning claims; preserve group-admin daily rows. |
| `internal/agentdocs/llms.txt`, `llms.txt` | Update canonical embedded agent text and byte-identical root mirror. |
| `mcpb/manifest.tmpl.json` | Correct the package description from the template source. |
| `.github/og-card.html`, `.github/og-card.png` | Correct source copy and regenerate the 1200×630 PNG from exact HTML. |
| `docs/assets/wa-demo.cast` | Replace the displayed `rate=2/30/1000` status with the amended short-window contract. |
| `issues.md`, `research.md` | Mark the known-recipient audit row and proposed daily status fields superseded/removed by Feature 113 without rewriting their historical ref context. |

Historical `specs/009-*` and `specs/016-code-quality-audit/*` artefacts remain
unchanged as prior-decision evidence. The tracked amendment is their explicit
supersession record.

## Design

```text
ordinary send
    |
    v
allowlist (unchanged)
    |
    v
2/second bucket, burst 2 --X--> existing rate/warmup refusal
    |
    v
30/minute bucket, burst 30 --X--> existing rate/warmup refusal
    |
    v
existing idempotency / delivery / adapter / audit path

No day bucket. No recipient map. No history lookup.
```

The conservative token ordering remains: if the second bucket accepts and the
minute bucket rejects, the second token stays consumed. This feature removes a
third bucket; it does not redesign token reservation.

## Test strategy

| Test/gate | Expected proof |
|---|---|
| New `TestSafety_NoPerRecipientDailyCap` | 31 same-JID sends at 2-second virtual intervals all pass; old code fails call 31. |
| New `TestRateLimiter_NoGlobalDailyCap` | 1,100 sends at 2-second virtual intervals all pass; old global bucket fails before completion even with refill. |
| Existing `TestSafety_RateLimitDeny` and `TestBurstExhaustion` | Immediate third send still fails. |
| Existing warmup/refill tests | 25%/50%/100%, minimum burst one, and refill semantics remain. |
| Existing `TestGroupCreateRateLimit5PerDay` | Sixth create remains a hard refusal. |
| Existing `TestAddParticipantsRateLimit50PerDay` | 51st add remains a hard refusal. |
| Source scan | No `KnownRecipientFunc`, `SetKnownRecipientFunc`, global day bucket, or recipient daily state remains. |
| Documentation scan | No living ordinary-send daily-cap claim remains; intentional group-admin and historical superseded claims are classified. |
| `TestMirrorsInSync` | Agent canonical/mirror pair stays byte-identical. |
| Full gates | Race/shuffle/no-cache suite, lint, native build, Nix flake check/build all pass. |

## Release and operational plan

1. Commit, push, and open the PR only after local gates.
2. Run full `zai/glm-5.3:max` review against the exact PR head. Any repair creates
   a new head and repeats review plus affected gates.
3. Wait for every required CI check and review thread to be green/resolved.
4. Stop `wa-deploy-sync.timer` and the Cafezinho replay timer before merge;
   verify neither can race the controlled rollout.
5. Squash-merge and record PR URL, merge SHA, merged timestamp, and state.
6. Push that exact merge SHA to `wa-burocracy` without force. Capture health
   before and after a real inbound event; require `staleState=healthy` and a
   strictly greater `lastEventTs`.
7. Only then push the same SHA to `wa-personal` and repeat the app-level check.
8. Verify exactly four matching Cafezinho rows are pending without printing
   payload content. Run the existing newest-selector once, verify its one ID
   becomes `delivered`, and inspect the matching downstream execution for the
   normal response branch and successful send rather than the fallback branch.
9. Ensure the replay timer is absent/cancelled and
   `/run/cafezinho-replay-after-reset.sh` is absent. Restore the continuous
   deploy timer only after both apps report the same deployed merge SHA and a
   sync probe says both are in sync.
10. Save test, review, merge, deploy, liveness, replay, and cleanup receipts.

No logout, session wipe, QR flow, re-pair, force-push, admin merge, or direct
push to repository `main` is permitted.

## Rollback

Rollback is one PR that reverts the squash merge SHA. It must pass the same
required gates and different-family review. Stop continuous deploy before its
merge, deploy the rollback merge SHA to bureaucracy, verify advancing
app-level liveness, then deploy personal. Do not mutate session or history
state. The one-line source reversal is:

```bash
git revert -s <feature-113-squash-merge-sha>
```

The revert is created in a dedicated numbered worktree/branch and merged through
normal branch protection; the command is not run on `main`.

## Constitution check — post-design

All pre-design gates remain PASS. Design adds no unknown entity, port, API,
dependency, schema, or migration; [data-model.md](./data-model.md) is complete.
The release sequence satisfies real-liveness and rollback requirements, and
task generation can remain at or below 25 executable items.

## Contracts

No contract file is generated. The JSON-RPC/REST/MCP request and response
schemas, method names, error codes, and CLI flags are unchanged; only the
observable policy decision for legally paced ordinary sends changes.
