# Runbook: whatsmeow upstream bump

When Renovate opens a PR titled `fix(deps): update whatsmeow`, this is the
procedure to validate it before merging.

## 1. Read the upstream commit range

The PR body is configured (`renovate.json` packageRule, `fetchChangeLogs:
branch`) to embed the upstream commit list. If it is missing or truncated:

```bash
OLD=$(git show main:go.mod | grep go.mau.fi/whatsmeow | awk '{print $2}')
NEW=$(git show HEAD:go.mod | grep go.mau.fi/whatsmeow | awk '{print $2}')
gh repo clone tulir/whatsmeow /tmp/whatsmeow-upstream
git -C /tmp/whatsmeow-upstream log --oneline "${OLD#*-}..${NEW#*-}"
```

Skim every commit subject. The signals that matter:

- protocol changes (`waE2E`, `waBinary`, `binary/proto/`) → re-run integration
- new event types in `types/events/` → check `translate_event.go` switch
- removed/renamed `Client.*` methods → may break our 12-method interface in
  `whatsmeow_client.go`
- store schema changes (`store/sqlstore/upgrade.go`) → flag for manual pair test

## 2. Re-run the unit suite

```bash
go test -race -count=1 ./...
go vet ./...
golangci-lint run
```

The contract suite under `internal/adapters/secondary/whatsmeow/` runs against
the hand-rolled fake client. If the fake fails to compile, our 12-method
interface drifted from upstream — update `whatsmeow_client.go` and the fake.

## 3. Re-run the integration contract suite (burner only)

If a paired burner number is available:

```bash
WA_INTEGRATION=1 go test -race -tags integration -run Contract \
  ./internal/adapters/secondary/whatsmeow/...
```

This is the only path that exercises the real websocket. If `TestPairRestartReconnect`
fails, the pairing flow regressed and merging is unsafe.

## 4. Manual `pairtest` harness

If schema or pairing code changed upstream, run the manual harness against a
fresh `session.db`:

```bash
rm -f ~/.local/share/wa/session.db
go run -tags integration ./internal/adapters/secondary/whatsmeow/internal/pairtest
```

Scan the QR. Expected: `paired ok` within 60 seconds, `session.db` created.

## 5. Merge or rollback

- All green → squash-merge the Renovate PR.
- Any red → comment on the PR with the failing test, do **not** force-merge.
  Rollback is `git revert` of the merge commit; the previous pseudo-version is
  restored automatically because `go.sum` is part of the revert.

## 6. Post-merge

Watch the next CI run on `main`. If a downstream consumer (`wad`) fails to
build, the breakage is in our adapter, not whatsmeow — fix forward.
