# Quickstart: Code Quality Audit & Modernization (016)

**Time to verify**: ~3 minutes on a fresh clone

## Prerequisites

- Go 1.26.1+ installed
- golangci-lint v2.6+ installed (`brew install golangci-lint` or `go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@latest`)

## Verify the refactoring

```bash
# 1. Checkout the branch
git checkout 016-code-quality-audit

# 2. Build both binaries (confirms no compilation errors)
go build ./cmd/wa ./cmd/wad

# 3. Run full test suite with race detector
go test -race ./...

# 4. Run linter with updated config (17+ linters)
golangci-lint run

# 5. Verify no mixed error wrapping patterns remain
grep -rn '%w.*%v\|%v.*%w' internal/ cmd/ && echo "FAIL: mixed wrapping found" || echo "PASS: clean wrapping"

# 6. Run fuzz target (30 seconds)
go test -fuzz=FuzzJIDParse ./internal/domain/ -fuzztime=30s

# 7. Run benchmarks
go test -bench=. -benchmem ./internal/app/

# 8. Verify cognitive complexity
# (requires gocognit: go install github.com/uudashr/gocognit/cmd/gocognit@latest)
gocognit -over 20 ./internal/ ./cmd/ && echo "FAIL: functions over threshold" || echo "PASS: all under 20"
```

## Expected results

- Step 2: Both `wa` and `wad` binaries build without errors
- Step 3: All tests pass with no race conditions detected
- Step 4: `golangci-lint run` exits 0 with 17+ linters active
- Step 5: Zero matches for mixed `%w`/`%v` patterns
- Step 6: Fuzz target runs for 30s without crashes
- Step 7: Benchmark output with stable ns/op values
- Step 8: No function exceeds cognitive complexity 20

## Key changes to verify manually

- `internal/app/eventbridge.go`: Waiter iteration no longer holds mutex during channel sends
- `internal/adapters/secondary/whatsmeow/adapter.go`: Adapter struct split into sub-structs (≤7 fields each)
- `internal/adapters/primary/socket/server.go`: Shutdown logic extracted to coordinator type
- `internal/app/errors.go`: Uses `errors.AsType` instead of `errors.As`
- `.golangci.yml`: 10 new linters added with settings
