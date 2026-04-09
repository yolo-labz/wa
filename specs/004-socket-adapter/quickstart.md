# Quickstart: Socket Primary Adapter

**Feature**: 004-socket-adapter
**Goal**: reproduce the feature-complete state of `internal/adapters/primary/socket/` from a fresh clone in under 5 minutes.

This quickstart is the smoke test that will run at the end of implementation. It assumes you have Go 1.25+ installed, have cloned `yolo-labz/wa`, and are on the `004-socket-adapter` branch.

## 0. Prerequisites

```bash
go version          # expect 1.25 or newer
git status          # expect "On branch 004-socket-adapter"
```

## 1. Add dependencies

```bash
go get github.com/creachadair/jrpc2
go get go.uber.org/goleak
go mod tidy
```

Verify `go.mod` now contains both modules. `golang.org/x/sys/unix`, `github.com/rogpeppe/go-internal/lockedfile`, and `github.com/adrg/xdg` are already present from features 001–003.

## 2. Build the socket package

```bash
go build ./internal/adapters/primary/socket/...
```

Expected: clean build. If this fails, the OS-specific build-tagged files (`path_*.go`, `peercred_*.go`) are incorrectly tagged.

## 3. Run the contract suite against the fake dispatcher

```bash
go test -race -count=1 ./internal/adapters/primary/socket/...
```

Expected: all tests pass. Every FR in `spec.md` is covered by at least one test; every row in `contracts/wire-protocol.md` is exercised.

## 4. Run the goroutine-leak sweep

The `sockettest` package wires `goleak.VerifyTestMain(m)` in its `TestMain`. A clean test run proves zero leaked goroutines.

```bash
go test -race -count=3 ./internal/adapters/primary/socket/sockettest/...
```

The `-count=3` ensures the TestMain-level leak check runs three full iterations; a flaky goroutine would surface.

## 5. Lint

```bash
golangci-lint run ./internal/adapters/primary/socket/...
```

Expected: zero findings. The `core-no-whatsmeow` depguard rule continues to pass; the new `adapters-primary-no-whatsmeow` rule added by this feature also passes.

## 6. Benchmark the roundtrip

```bash
go test -bench=BenchmarkRoundtrip -benchmem ./internal/adapters/primary/socket/...
```

Expected: each op under 10 ms (matches SC-001), allocations bounded (no obvious leaks).

## 7. Smoke-test the single-instance guarantee

A small Go program in `internal/adapters/primary/socket/internal/doubleboot/main.go` launches two servers with the same socket path and asserts the second returns `ErrAlreadyRunning`.

```bash
go run ./internal/adapters/primary/socket/internal/doubleboot
```

Expected output:

```
server 1: listening on /tmp/wa-quickstart.sock
server 2: ErrAlreadyRunning (as expected)
server 1: shutdown complete
```

## 8. Smoke-test the peer-cred gate

(Manual, OS-specific.) If you are on a multi-user system, you can verify the peer-cred gate by running the same `doubleboot` smoke test while connecting as a different user:

```bash
# terminal 1 (as your user)
go run ./internal/adapters/primary/socket/internal/doubleboot --hold

# terminal 2 (as a different user)
nc -U /tmp/wa-quickstart.sock
# expect: immediate disconnect, no bytes read, a log line in terminal 1
```

This step is optional and documented as a manual verification; CI runs it as an in-process test with a mocked UID check via `os.Geteuid`.

## 9. Verify the wire protocol against the contract

```bash
bash specs/004-socket-adapter/scripts/verify-wire-protocol.sh
```

(This script is created during implementation. It parses `contracts/wire-protocol.md` for every error code entry and greps the Go source for matching constants in `errcodes.go`, failing if anything is missing on either side.)

## 10. Full feature gate

```bash
go test -race ./... \
  && golangci-lint run ./... \
  && go vet ./... \
  && echo "feature 004 ready for merge"
```

Expected: the final line prints. This is the same gate feature 003 shipped with and is the minimum bar for merging.

---

## Troubleshooting

| Symptom | Likely cause | Fix |
|---|---|---|
| `socket path too long` | `$HOME` is very long, darwin `sun_path` limit | Set `TMPDIR` to a shorter path for tests; in production, use a shorter login name |
| `bind: permission denied` | `$XDG_RUNTIME_DIR` does not exist on a headless Linux host | Export `XDG_RUNTIME_DIR=/tmp/wa-runtime` in the daemon's environment |
| `operation not supported` on darwin from `GetsockoptUcred` | Wrong build tag — `peercred_linux.go` was compiled on darwin | Check the build tag header; it must be `//go:build linux` not `//go:build unix` |
| Zero-downtime deploy corruption | ES holds an exclusive lock; not relevant to feature 004 but inherited from 003 | Leave `dokku checks:disable` for whatsmeow-adjacent apps |
| Goleak reports a leaked goroutine from `jrpc2` | jrpc2's server has a background goroutine that must be stopped via `Stop()` before shutdown returns | Verify the shutdown sequence in `lifecycle.go` calls `rpcServer.Stop()` before `wg.Wait()` |

---

## What this quickstart does NOT cover

Out of scope per `spec.md`:

- Real WhatsApp events — feature 005 will add these via the production dispatcher
- `cmd/wad` binary and its systemd/launchd integration — feature 006/007
- `cmd/wa` CLI client — feature 006
- TLS, token auth, multi-user access — not in scope for v0.1

If you need to reproduce an end-to-end pair + send flow, stop here and wait for feature 006.
