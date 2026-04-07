# Quickstart: Verify feature 002 in five minutes

**Branch**: `002-domain-and-ports` · **Plan**: [`plan.md`](./plan.md) · **Spec**: [`spec.md`](./spec.md)

This is the executable form of [`checklists/requirements.md`](./checklists/requirements.md) items CHK029–CHK037 plus success criteria SC-001 through SC-008. A fresh contributor with `git`, `go`, and `golangci-lint` should be able to clone, run every block, and finish with green output in under five minutes — once `/speckit:implement` lands the code.

## 0. Prerequisites

```sh
command -v git            >/dev/null || { echo "install git"; exit 1; }
command -v go             >/dev/null || { echo "install Go (>= 1.22)"; exit 1; }
command -v golangci-lint  >/dev/null || { echo "install golangci-lint v2.x"; exit 1; }
go version | grep -qE 'go1\.(2[2-9]|[3-9][0-9])' || { echo "Go 1.22+ required"; exit 1; }
golangci-lint version | grep -q ' 2\.' || { echo "golangci-lint v2 required (the .golangci.yml uses the v2 schema)"; exit 1; }
```

## 1. Clone

```sh
git clone https://github.com/yolo-labz/wa.git /tmp/wa-002
cd /tmp/wa-002
git fetch origin 002-domain-and-ports
git checkout 002-domain-and-ports
```

Expected: clean checkout, `git status -s` prints nothing.

## 2. Verify the source tree shape

```sh
required_files=(
  internal/domain/errors.go
  internal/domain/ids.go
  internal/domain/action.go
  internal/domain/jid.go
  internal/domain/contact.go
  internal/domain/group.go
  internal/domain/message.go
  internal/domain/event.go
  internal/domain/session.go
  internal/domain/allowlist.go
  internal/domain/audit.go
  internal/app/ports.go
  internal/app/porttest/suite.go
  internal/adapters/secondary/memory/adapter.go
  internal/adapters/secondary/memory/clock.go
  internal/adapters/secondary/memory/adapter_test.go
)
for f in "${required_files[@]}"; do
  test -f "$f" || { echo "FAIL: missing $f"; exit 1; }
done

# Forbidden contents from spec FR-016, FR-017
test "$(git diff main..HEAD -- cmd/ | wc -l)" -eq 0       # cmd/ untouched
test ! -e internal/adapters/secondary/whatsmeow/*.go      # no whatsmeow code yet
test ! -e internal/adapters/primary/socket/*.go           # no daemon code yet

# Untouched .gitkeeps
required_gitkeeps=(
  cmd/wa/.gitkeep
  cmd/wad/.gitkeep
  internal/adapters/primary/socket/.gitkeep
  internal/adapters/secondary/whatsmeow/.gitkeep
  internal/adapters/secondary/sqlitestore/.gitkeep
  internal/adapters/secondary/slogaudit/.gitkeep
)
for f in "${required_gitkeeps[@]}"; do
  test -f "$f" || { echo "FAIL: missing $f (must survive feature 002)"; exit 1; }
done

echo "tree ok"
```

Expected: prints `tree ok`. Validates spec FR-016, FR-017 and checklist CHK029, CHK036.

## 3. Verify the hexagonal invariant (the most important step)

```sh
# Negative case: there should be ZERO whatsmeow imports under domain or app
violations=$(grep -rIlE 'go\.mau\.fi/whatsmeow' internal/domain internal/app 2>/dev/null | wc -l)
test "$violations" -eq 0 || { echo "FAIL: $violations whatsmeow imports in core"; exit 1; }

# Positive case: introduce a deliberate violation and confirm depguard catches it
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

Expected: prints `depguard catches violation OK`. Validates spec FR-009, SC-003, SC-004, and checklist CHK033.

## 4. Run the linter

```sh
golangci-lint run ./...
```

Expected: exits 0, prints nothing (or only configuration noise on stderr). Validates spec FR-014, SC-003.

## 5. Run the tests

```sh
go test -race -count=1 ./...
```

Expected: exits 0, all tests pass, the race detector reports no races, total wall time under 5 seconds. Validates spec FR-013, SC-002.

The output should mention at minimum:

- `internal/domain` test files passing (~76 tests)
- `internal/app/porttest` contract suite passing against the in-memory adapter (~30 tests)
- `internal/adapters/secondary/memory` adapter test invoking `porttest.RunContractSuite` and reporting OK

## 6. Verify the contract suite is reusable

This is the test that will be re-run by feature 003 against the whatsmeow adapter; this run only confirms it can be invoked from outside `porttest/` package.

```sh
cat <<'GO' > /tmp/wa-002/scratch_consumer_test.go
package consumer_test

import (
    "testing"

    "github.com/yolo-labz/wa/internal/adapters/secondary/memory"
    "github.com/yolo-labz/wa/internal/app/porttest"
)

func TestExternalConsumerCanRunSuite(t *testing.T) {
    porttest.RunContractSuite(t, func(t *testing.T) porttest.Adapter {
        return memory.NewAdapter()
    })
}
GO
go test ./scratch_consumer_test.go
rm /tmp/wa-002/scratch_consumer_test.go
```

Expected: the scratch test passes. Validates spec FR-011, SC-007 and confirms US4 acceptance scenario 1.

## 7. Verify `go vet` is clean

```sh
go vet ./...
```

Expected: exits 0, prints nothing. Validates spec SC-008.

## 8. Walk the JSON-RPC port mapping (SC-001 rehearsal)

Open [`contracts/ports.md`](./contracts/ports.md) and find the table at the bottom titled "Mapping to JSON-RPC methods". Without looking at any other file, name the port that backs each of the 11 methods. If you can do all 11 in under 10 minutes, SC-001 is met. If you cannot, file an issue against the docs — the spec promises a 10-minute reading.

## 9. (Optional) Tear down

```sh
cd / && rm -rf /tmp/wa-002
```

## What this quickstart does NOT cover

- **Pairing a real WhatsApp number** — there is no whatsmeow code in this feature. Reach this step in feature 003 (`whatsmeow secondary adapter`).
- **Sending a real message** — same. The `MessageSender` port is implemented only by the in-memory adapter, which records sends to a slice.
- **Running the daemon** — `wad` does not exist yet. Reach this step in feature 004 (`daemon and JSON-RPC socket`).
- **Building the `wa` binary** — `cmd/wa/main.go` does not exist yet. Reach this step in feature 005 (`wa CLI client`).
- **Releasing or notarizing** — `goreleaser` does not run on this branch. Reach this step in feature 006 (`distribution`).

This quickstart deliberately stops at "the hexagonal core compiles, lints clean, tests green, and proves the depguard rule works." Anything beyond that belongs to a later feature's quickstart.
