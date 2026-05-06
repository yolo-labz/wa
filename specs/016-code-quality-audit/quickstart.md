# Quickstart: Code Quality Audit & Modernization (016)

**Time to verify**: ~3 minutes on a fresh clone

## Prerequisites

- Go 1.26.1+ installed
- golangci-lint v2.6+ installed (`brew install golangci-lint` or `go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@latest`)

## Verify the refactoring

```bash
# 1. Checkout main (016 has been incrementally merged across PR #115-#126)
git checkout main && git pull --ff-only origin main

# 2. Build both binaries (confirms no compilation errors)
go build ./cmd/wa ./cmd/wad

# 3. Run full test suite with race detector
go test -race -count=1 ./...

# 4. Verify go.sum integrity (FR-016 / T071)
go mod verify

# 5. Run linter (golangci-lint pinned to v2.6.0 in CI)
golangci-lint run

# 6. Verify no mixed error wrapping patterns remain (SC-006 / T086)
grep -rn '%w.*%v\|%v.*%w' internal/ cmd/ && echo "FAIL: mixed wrapping found" || echo "PASS: clean wrapping"

# 7. Verify os.Exit only in main() (SC-010 / T087)
matches="$(grep -rn 'os.Exit' cmd/wa/ | grep -v _test.go | grep -v '// ')"
if [ "$matches" = "cmd/wa/main.go:13:	os.Exit(run())" ]; then echo PASS; else echo "FAIL: $matches"; fi

# 8. Run fuzz target (30 seconds; expect ~10 M execs / 0 crashes on M-class CPU)
go test -fuzz=FuzzParse ./internal/domain/ -fuzztime=30s

# 9. Run rate-limiter benchmarks (FR-009)
go test -run=XXXNEVER -bench=BenchmarkRateLimiter -benchmem ./internal/app/

# 10. Run FTS5 benchmarks (FR-014, requires WA_BENCH=1 opt-in)
WA_BENCH=1 go test -run=XXXNEVER -bench='Benchmark(QuerySearch|QueryHistory|InsertBatch)' \
  -benchmem -count=3 ./internal/adapters/secondary/sqlitehistory/

# 11. Verify SQLite security pragmas (FR-014 / T089). Pragma values flow
# through the DSN, so probe a fresh ephemeral DB with the same pragmas:
go run /dev/stdin <<'EOF'
package main
import (
    "database/sql"; "fmt"; "os"
    _ "modernc.org/sqlite"
)
func main() {
    tmp, _ := os.MkdirTemp("", "wa-pragma-"); defer os.RemoveAll(tmp)
    for _, db := range []string{"messages.db", "session.db"} {
        dsn := "file:" + tmp + "/" + db +
            "?_pragma=trusted_schema(OFF)&_pragma=cell_size_check(ON)"
        d, _ := sql.Open("sqlite", dsn)
        var t, c int
        _ = d.QueryRow("PRAGMA trusted_schema").Scan(&t)
        _ = d.QueryRow("PRAGMA cell_size_check").Scan(&c)
        fmt.Printf("%s trusted_schema=%d cell_size_check=%d\n", db, t, c)
        _ = d.Close()
    }
}
EOF
# Expected:
#   messages.db trusted_schema=0 cell_size_check=1
#   session.db  trusted_schema=0 cell_size_check=1

# 12. Verify audit-log HMAC chain (FR-015 / T053-T055).
# After running wad and recording at least one audit event:
wa audit verify
# Expected: ok: <N> records verified at <path>
# A non-zero exit names the offending line number.

# 13. Verify cognitive complexity (gocognit -over 20). Production trigger:
# only `cmd/wad/main.go run` is over (tracked for T077-T081). Test
# contracts and case-enumeration helpers are excluded by intent.
gocognit -over 20 ./internal/app/ ./internal/domain/ ./internal/adapters/

# 14. Verify coverage thresholds at the FR-020 ratchet floors.
go test -count=1 -covermode=atomic -coverprofile=cover.out ./...
DOMAIN_THRESHOLD=60 APP_THRESHOLD=50 ADAPTERS_THRESHOLD=55 \
  bash .github/scripts/check-coverage.sh
```

## Expected results

- Step 2: Both `wa` and `wad` binaries build without errors
- Step 3: All 16 packages green; race detector clean
- Step 4: `all modules verified`
- Step 5: `golangci-lint run` exits 0
- Step 6: PASS — no mixed `%w`/`%v` patterns
- Step 7: PASS — only `cmd/wa/main.go:13`
- Step 8: ~10 M execs in 30 s, 0 crashes
- Step 9: `Allow` ≤ 800 ns/op / 0 allocs across all warmup tiers; `AllowFor` ~12 µs/op / 1 alloc
- Step 10: `QuerySearch` ~25 ms/op (SC-004 < 200 ms easily met); compare to `specs/016-code-quality-audit/fts5-baseline-mmap268435456.txt` (local-only)
- Step 11: Both DBs print `trusted_schema=0 cell_size_check=1`
- Step 12: `wa audit verify` exits 0 with verified-record count
- Step 13: Only test contracts and `cmd/wad/main.go run` over threshold (production fix tracked in T077-T081)
- Step 14: Domain ≥ 60 %, app ≥ 50 %, adapters ≥ 55 % (FR-020 ratchet floors)

## Key changes to verify manually (all merged)

- `internal/app/eventbridge.go`: Waiter iteration no longer holds mutex during channel sends (PR #116 era)
- `internal/adapters/secondary/sqlitestore/store.go`: `trusted_schema=OFF` + `cell_size_check=ON` + `quick_check` at startup (PR #116)
- `internal/adapters/secondary/sqlitehistory/store.go`: same SQLite security pragmas; `closeRows` slog.Warn helper (PR #116, #117)
- `internal/adapters/secondary/slogaudit/chain.go` + `audit.go`: tamper-evident HMAC-SHA-256 hash chain on every line (PR #122)
- `internal/domain/audit.go`: `AuditEvent.Source` field + `NewAuditEventFrom(source, …)` (PR #121)
- `internal/domain/jid.go`, `message.go`, `session.go`: `slog.LogValuer` implementations (PR #119, #120)
- `cmd/wa/cmd_audit.go`: `wa audit verify` subcommand (PR #122)
- `.github/workflows/ci.yml`: `go mod verify`, `GOFLAGS=-mod=readonly`, coverage thresholds, golangci-lint pinned to v2.6.0 (PR #121, #125, #126)
- `.github/workflows/fuzz.yml`: nightly 5-target fuzz with crashers artifact (PR #119/#120)
