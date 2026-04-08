# Quickstart: Verify feature 003 in five minutes

**Branch**: `003-whatsmeow-adapter` · **Plan**: [`plan.md`](./plan.md) · **Spec**: [`spec.md`](./spec.md)

This is the executable form of [`checklists/requirements.md`](./checklists/requirements.md) items CHK037–CHK045 plus success criteria SC-001 through SC-009. A fresh contributor with `git`, `go`, `golangci-lint`, AND **a paired WhatsApp burner number** should be able to clone, run every block, and finish with green output in under five minutes (excluding the manual pairing step which takes ~60s).

## 0. Prerequisites

```sh
command -v git           >/dev/null || { echo "install git"; exit 1; }
command -v go            >/dev/null || { echo "install Go (>=1.22)"; exit 1; }
command -v golangci-lint >/dev/null || { echo "install golangci-lint v2.x"; exit 1; }
command -v jq            >/dev/null || { echo "install jq"; exit 1; }
go version | grep -qE 'go1\.(2[2-9]|[3-9][0-9])' || { echo "Go 1.22+ required"; exit 1; }
```

A burner WhatsApp number is required for steps 6 and 7 (integration tests). Steps 1-5 do not need one.

## 1. Clone

```sh
git clone https://github.com/yolo-labz/wa.git /tmp/wa-003
cd /tmp/wa-003
git fetch origin 003-whatsmeow-adapter
git checkout 003-whatsmeow-adapter
```

## 2. Verify the architectural invariant — zero unexpected core changes

```sh
# Two specific permitted modifications:
# - one new var ErrDisconnected line in internal/domain/errors.go
# - one new HistoryStore interface block in internal/app/ports.go
# Anything else under internal/domain/ or internal/app/ports.go is a SC-002 violation.

git diff main..HEAD -- internal/domain/errors.go | grep -E '^\+' | grep -v '^\+\+\+' | wc -l
# expected: ≤ 3 (the new var + a doc comment line + maybe a blank line)

git diff main..HEAD -- internal/app/ports.go | grep -E '^\+' | grep -v '^\+\+\+' | wc -l
# expected: ≤ ~15 (the HistoryStore interface block + doc comment)

# No other domain or app file may be modified by this feature.
git diff main..HEAD --name-only -- internal/domain/ internal/app/ \
  | grep -vE '^(internal/domain/errors\.go|internal/domain/errors_test\.go|internal/app/ports\.go|internal/app/porttest/historystore\.go)$' \
  | wc -l
# expected: 0
```

Validates spec SC-002 and the architectural test of US1.

## 3. Verify the depguard rule still fires (regression test for the hexagonal invariant)

```sh
echo 'package domain
import _ "go.mau.fi/whatsmeow"' > internal/domain/violation.go
if golangci-lint run ./internal/domain/... 2>&1 | grep -q core-no-whatsmeow; then
  echo "depguard catches violation OK"
  rm internal/domain/violation.go
else
  rm internal/domain/violation.go
  echo "FAIL: depguard rule core-no-whatsmeow did NOT block the violation"
  exit 1
fi
```

Validates spec FR-009, SC-003, FR-022.

## 4. Verify the new files exist

```sh
required=(
  internal/domain/errors.go
  internal/app/ports.go
  internal/app/porttest/historystore.go
  internal/adapters/secondary/whatsmeow/adapter.go
  internal/adapters/secondary/whatsmeow/pair.go
  internal/adapters/secondary/whatsmeow/send.go
  internal/adapters/secondary/whatsmeow/stream.go
  internal/adapters/secondary/whatsmeow/translate_jid.go
  internal/adapters/secondary/whatsmeow/translate_event.go
  internal/adapters/secondary/whatsmeow/history.go
  internal/adapters/secondary/whatsmeow/flags.go
  internal/adapters/secondary/whatsmeow/adapter_integration_test.go
  internal/adapters/secondary/sqlitestore/store.go
  internal/adapters/secondary/sqlitehistory/store.go
  internal/adapters/secondary/sqlitehistory/schema.sql
  internal/adapters/secondary/sqlitehistory/schema_embed.go
)
for f in "${required[@]}"; do
  test -f "$f" || { echo "FAIL: missing $f"; exit 1; }
done
echo "files ok"
```

## 5. Run the unit tests (no burner number needed)

```sh
go test -race -count=1 ./internal/adapters/secondary/whatsmeow/... \
                       ./internal/adapters/secondary/sqlitestore/... \
                       ./internal/adapters/secondary/sqlitehistory/... \
                       ./internal/app/... \
                       ./internal/domain/...
```

Expected: exits 0, all tests pass, race detector reports no races. The `whatsmeow` package's tests use the `whatsmeowClient` interface fake from research §D4, NOT a real WhatsApp connection.

```sh
golangci-lint run ./...
go vet ./...
```

Both exit 0. Validates spec SC-003, FR-013, FR-014.

## 6. (Burner number required) Run the contract test suite against the whatsmeow adapter

```sh
# Pair the burner first using the manual harness:
WA_INTEGRATION=1 go run ./internal/adapters/secondary/whatsmeow/internal/pairtest/
# Scan the half-block QR code with your burner phone (within 60s).
# The harness exits 0 once paired and ~/.local/share/wa/session.db exists.

# Then run the full contract suite:
WA_INTEGRATION=1 go test -race -tags integration -run Contract \
  ./internal/adapters/secondary/whatsmeow/...
```

Expected: every contract test from `internal/app/porttest/` passes against the whatsmeow adapter. The test count MUST equal the count for the in-memory adapter from feature 002 (modulo HS2 vs HS3 — the whatsmeow adapter exercises HS2 remote-backfill, the in-memory adapter exercises HS3 empty-success).

Validates spec SC-001, US1 acceptance scenarios 1-3, US2 acceptance scenarios 1-3.

## 7. (Burner number required) Verify the single-instance flock

```sh
# Start one pairing harness (already paired from step 6):
WA_INTEGRATION=1 go run ./internal/adapters/secondary/whatsmeow/internal/pairtest/ &
HARNESS_PID=$!
sleep 2  # let it acquire the flock

# Try to start a second instance — should fail immediately:
WA_INTEGRATION=1 go run ./internal/adapters/secondary/whatsmeow/internal/pairtest/ 2>&1 | grep -q 'session locked'
echo "second-instance blocked: ok"

kill $HARNESS_PID
wait $HARNESS_PID 2>/dev/null
```

Validates spec SC-004, US3 acceptance scenarios 1-3.

## 8. Verify the file permissions

```sh
test "$(stat -f %p ~/.local/share/wa/session.db | tail -c 4)" = "600"  || echo FAIL session.db perm
test "$(stat -f %p ~/.local/share/wa/messages.db | tail -c 4)" = "600" || echo FAIL messages.db perm
test "$(stat -f %p ~/.local/share/wa | tail -c 4)" = "700"             || echo FAIL data dir perm
```

Validates spec FR-006, US3 acceptance scenario 4.

## 9. (Burner number required) Sanity-check the bounded history sync

```sh
# After pairing in step 6, inspect messages.db
sqlite3 ~/.local/share/wa/messages.db "SELECT COUNT(*) FROM messages"
# expected: > 0, < ~5000 (7-day window for an active personal account)

du -h ~/.local/share/wa/messages.db
# expected: single-digit MB (FR-019 cap was 20 MB)
```

Validates spec FR-019, the Clarifications session 2026-04-07 decision.

## 10. (Optional) Tear down

```sh
cd / && rm -rf /tmp/wa-003
```

## What this quickstart does NOT cover

- **Daemon process** — `wad` does not exist yet; lands in feature 004
- **CLI client** — `wa send`, `wa status`, etc., do not exist yet; lands in feature 005
- **JSON-RPC socket** — no IPC layer in this feature
- **Rate limiter / warmup / audit log file** — feature 004
- **Claude Code plugin** — separate repo (`wa-assistant`), feature 007
- **Distribution** — `goreleaser` does not run on this branch

This quickstart deliberately stops at "the whatsmeow adapter compiles, lints clean, the contract suite passes against a real WhatsApp account, and the bounded history sync produces a single-digit-MB messages.db." Anything beyond that is a later feature's quickstart.
