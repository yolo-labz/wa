# Quickstart: Application Use Cases

**Feature**: 005-app-usecases
**Goal**: Reproduce the feature-complete state of `internal/app/` from a fresh clone in under 5 minutes.

## 0. Prerequisites

```bash
go version          # expect 1.25+
git status          # expect "On branch 005-app-usecases"
```

## 1. Add dependencies

```bash
go get golang.org/x/time/rate
go mod tidy
```

`x/time` may already be transitively present. Verify `go.sum` contains it.

## 2. Verify ports.go change

```bash
grep 'MarkRead' internal/app/ports.go
```

Expected: `MarkRead(ctx context.Context, chat domain.JID, id domain.MessageID) error` is present on `MessageSender`.

## 3. Build the app package

```bash
go build ./internal/app/...
```

Expected: clean build. The new files (`dispatcher.go`, `method_*.go`, `safety.go`, `ratelimiter.go`, `eventbridge.go`, `events.go`, `errors.go`) compile without errors.

## 4. Run the unit tests

```bash
go test -race -count=1 ./internal/app/...
```

Expected: all tests pass. Tests use in-memory fakes only — no whatsmeow, no real WhatsApp, no network.

## 5. Verify safety pipeline

```bash
go test -race -run TestSafety ./internal/app/...
```

Expected: allowlist deny, rate limit, and warmup tests all pass. The rate limiter correctly rejects the (N+1)th request per bucket.

## 6. Verify event bridge

```bash
go test -race -run TestEventBridge ./internal/app/...
```

Expected: bridge delivers events from the in-memory EventStream to the Events() channel. Shutdown closes the channel. No goroutine leaks (goleak).

## 7. Lint

```bash
golangci-lint run ./internal/app/...
```

Expected: zero findings. `core-no-whatsmeow` depguard rule passes — zero `go.mau.fi/whatsmeow` imports in `internal/app/`.

## 8. Full gate

```bash
go test -race ./... && go vet ./... && echo "feature 005 ready for merge"
```

Expected: all packages pass, including the existing socket adapter and whatsmeow tests.

---

## What this quickstart does NOT cover

- Real WhatsApp events — requires the whatsmeow adapter (feature 003) wired by `cmd/wad` (feature 006).
- The composition-root adapter that converts `app.AppEvent` → `socket.Event` — feature 006.
- `cmd/wad` and `cmd/wa` binaries — feature 006.
- Persistent allowlist mutation (`allow` method) — feature 006.
- Device unlinking (`panic` method) — feature 006.
