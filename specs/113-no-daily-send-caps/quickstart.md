# Feature 113 — verification, release, and rollback quickstart

This is the implementation/release runbook. None of these commands is executed
during specification authoring.

## 1. Targeted red/green checks

After adding tests but before implementation, the two new daily-removal tests
must fail against the old limiter. After implementation:

```bash
go test ./internal/app -run 'TestSafety_NoPerRecipientDailyCap|TestRateLimiter_NoGlobalDailyCap|TestSafety_RateLimitDeny|TestBurstExhaustion' -count=1

go test ./internal/adapters/secondary/whatsmeow \
  -run 'TestGroupCreateRateLimit5PerDay|TestAddParticipantsRateLimit50PerDay' \
  -count=1
```

Static removal receipt:

```bash
test -z "$(git grep -n -E 'KnownRecipientFunc|SetKnownRecipientFunc|defaultPerDay|defaultPerRecipientDaily|defaultNewRecipientDaily|recipientDaily|newRecipientCnt' -- '*.go' || true)"
```

## 2. Full local gates

```bash
gofumpt -w internal/app/ratelimiter.go internal/app/ratelimiter_test.go \
  internal/app/safety.go internal/app/safety_test.go \
  internal/app/ratelimiter_bench_test.go internal/app/dispatcher.go \
  cmd/wad/main.go cmd/wad/history_methods.go

go test -race -shuffle=on -count=1 ./...
golangci-lint run
go build ./cmd/wa ./cmd/wad
nix flake check --no-build --accept-flake-config
nix build .#default --print-build-logs --accept-flake-config
./result/bin/wa version
./result/bin/wa --help | head -5
```

Agent mirror and living-claim checks:

```bash
cmp internal/agentdocs/llms.txt llms.txt
go test ./internal/agentdocs -run TestMirrorsInSync -count=1

git grep -n -i -E '1000/day|1,000 sends|per-recipient daily|new-recipient daily|2/30/1000' -- \
  README.md README.pt-BR.md SECURITY.md docs/manual.md docs/roadmap.md \
  internal/agentdocs/llms.txt llms.txt mcpb/manifest.tmpl.json \
  .github/og-card.html docs/assets/wa-demo.cast
```

The final grep must return no ordinary-send daily claim. Separate 5/day group
creation and 50/day participant-add claims remain expected.

Regenerate `.github/og-card.png` from `.github/og-card.html` with a 1200×630
headless-Chrome screenshot and verify its dimensions before the full diff is
reviewed. Do not hand-edit the PNG.

## 3. Exact-head review and merge receipt

Record the candidate SHA, run full `zai/glm-5.3:max` as the different-family
reviewer against that exact SHA, and repeat after any repair:

```bash
wa113_review_sha=$(git rev-parse HEAD)
git status --short
```

The worktree must be clean, the review receipt must name
`$wa113_review_sha`, all review threads must be resolved, and every required CI
check must pass.

Before merge, stop both timers so neither deployment nor replay can race the
manual rollout:

```bash
ssh ProxMox.Dokku \
  'sudo systemctl stop wa-deploy-sync.timer cafezinho-replay-after-rate-reset.timer; \
   ! sudo systemctl is-active --quiet wa-deploy-sync.timer; \
   ! sudo systemctl is-active --quiet cafezinho-replay-after-rate-reset.timer'
```

Then merge normally and capture the durable receipt:

```bash
gh pr merge <pr-number> --squash --delete-branch
gh pr view <pr-number> --json state,url,mergeCommit,mergedAt
```

Expected: `state` is `MERGED`. Never use `--admin`.

## 4. Manual bureaucracy canary

Fetch the exact merge SHA and push it to the existing Dokku deploy remote
without force:

```bash
git fetch origin main
wa113_merge_sha=$(gh pr view <pr-number> --json mergeCommit --jq '.mergeCommit.oid')
git push dokku-burocracy "$wa113_merge_sha:refs/heads/main"
```

Capture health twice around a real inbound application event:

```bash
ssh ProxMox.Dokku \
  'sudo docker exec wa-burocracy.web.1 /usr/local/bin/wa --json health'
```

The second receipt must have `paired:true`, `connected:true`,
`staleState:"healthy"`, and `lastEventTs` strictly greater than the first.
Socket connectivity alone is not a pass. Do not logout, wipe, or re-pair.

## 5. Manual personal deployment

Only after the bureaucracy gate passes:

```bash
git push dokku-personal "$wa113_merge_sha:refs/heads/main"
ssh ProxMox.Dokku \
  'sudo docker exec wa-personal.web.1 /usr/local/bin/wa --json health'
```

Repeat the advancing-`lastEventTs` and `staleState:"healthy"` gate before any
Cafezinho replay.

## 6. Replay exactly one Cafezinho delivery

The existing `/run/cafezinho-replay-after-reset.sh` contains the incident's
read-only selector. Before running it, inspect its selector in read-only mode
and record only delivery IDs, update timestamps, and states—never payloads.
Abort unless exactly four matching pending rows exist and the selected ID is
the maximum update timestamp.

Run the selector/replay script exactly once:

```bash
ssh ProxMox.Dokku 'sudo /run/cafezinho-replay-after-reset.sh'
```

The script must report one selected identifier and settle it to `delivered`.
Use `n8nctl runs 10` and the matching execution details to confirm the normal
model/cleaner/`wa Envia` path succeeded and the fallback branch was not taken.
Do not print the inbound or outbound message body in the receipt.

Final replay cleanup:

```bash
ssh ProxMox.Dokku \
  'sudo systemctl stop cafezinho-replay-after-rate-reset.timer 2>/dev/null || true; \
   sudo rm -f /run/cafezinho-replay-after-reset.sh; \
   ! sudo systemctl is-active --quiet cafezinho-replay-after-rate-reset.timer; \
   test ! -e /run/cafezinho-replay-after-reset.sh'
```

After both apps report the exact merge SHA and the deploy sync script reports
both in sync, restore the previously active continuous-deploy timer:

```bash
ssh ProxMox.Dokku \
  'sudo systemctl start wa-deploy-sync.timer && \
   sudo systemctl is-active --quiet wa-deploy-sync.timer'
```

## 7. Rollback

Create a dedicated numbered rollback worktree from current `origin/main`, then
make one signed revert commit:

```bash
git fetch origin main
git worktree add ../wa-<number>-revert-113 -b <number>-revert-113 origin/main
cd ../wa-<number>-revert-113
git revert -s <feature-113-squash-merge-sha>
```

Push, open one revert PR, pass the same gates/review, stop continuous deploy,
and merge through branch protection. Deploy the rollback merge SHA to
`dokku-burocracy` first, prove advancing app-level liveness, then deploy
`dokku-personal`. No session reset, history mutation, force-push, re-tag, or
direct repository-main push is part of rollback.
