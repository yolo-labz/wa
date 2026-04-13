# Code Quality Audit: Research — Bleeding-Edge Go Practices (2025-2026)

**Research date**: 2026-04-13
**Method**: 4-agent parallel web research swarm covering modern Go idioms, static analysis tools, concurrency patterns, and code readability best practices.

---

## Table of Contents

1. [Go 1.22-1.26 Language Features](#1-go-122-126-language-features)
2. [Modern Error Handling](#2-modern-error-handling)
3. [Static Analysis & Linting](#3-static-analysis--linting)
4. [Concurrency Patterns](#4-concurrency-patterns)
5. [Structured Logging (slog)](#5-structured-logging-slog)
6. [Testing Patterns](#6-testing-patterns)
7. [Interface Design](#7-interface-design)
8. [Code Readability & Style](#8-code-readability--style)
9. [Domain-Driven Design in Go](#9-domain-driven-design-in-go)
10. [Rate Limiting](#10-rate-limiting)
11. [Recommended Linter Additions](#11-recommended-linter-additions)
12. [Actionable Items Summary](#12-actionable-items-summary)
13. [Sources](#13-sources)

---

## 1. Go 1.22-1.26 Language Features

### Go 1.22: Range-over-int
`for i := range n` replaces `for i := 0; i < n; i++`. Eliminates off-by-one surface area.

### Go 1.22: Per-iteration loop variables
The `tt := tt` capture hack in parallel table tests is **no longer needed** for modules declaring `go 1.22` or later. Each iteration gets its own variable. Major readability win.

### Go 1.23: Range-over-func (iterators)
Three function signatures are valid as range targets:
- `func(func() bool)` — zero values
- `func(func(K) bool)` — single value (`iter.Seq[V]`)
- `func(func(K, V) bool)` — key-value pair (`iter.Seq2[K, V]`)

Standard library additions: `slices.All`, `slices.Values`, `slices.Collect`, `maps.Keys`, `maps.Values`, `bytes.Lines`, `strings.SplitSeq`, `strings.FieldsSeq`, `iter.Pull`/`iter.Pull2`.

**Critical rule**: Always check the return value of `yield()`. Ignoring it causes a panic if the caller breaks out of the range loop.

**Applicability**: The `EventStream` port is pull-based. If internal iteration helpers are needed (filtering groups, iterating allowlist entries), `iter.Seq` is the idiomatic return type. Future `SearchMessages` could return `iter.Seq2[domain.Message, error]` for lazy pagination.

### Go 1.24: Generic type aliases
Type aliases with type parameters: `type Container[T any] = newpkg.Container[T]`. Enables seamless package migrations without breaking callers.

### Go 1.25: testing/synctest (GA)
Already adopted in 4 test files. Creates isolated "bubbles" with fake time. `synctest.Wait()` blocks until all goroutines are durably blocked. Time advances only when all goroutines are quiesced — deterministic, no `time.Sleep` flakiness.

**Limitations**: Real I/O (network, filesystem) is NOT durably blocking. Socket tests must use real timeouts (documented at `sockettest/shutdown_test.go:17-22`).

### Go 1.26: `new(value)` syntax
`new(42)` returns `*int` pointing to 42. Eliminates the `ptr[T]` helper pattern. Useful for optional pointer fields in JSON-RPC result structs.

### Go 1.26: `errors.AsType[E]`
Generic, type-safe replacement for `errors.As`:
```go
// Before
var pathErr *fs.PathError
if errors.As(err, &pathErr) { ... }

// After (Go 1.26) — no reflect, ~3x faster, 1 fewer allocation
if pathErr, ok := errors.AsType[*fs.PathError](err); ok { ... }
```

**Direct applicability**: `IsCodedError` at `internal/app/errors.go:46-52` can be modernized from `errors.As` to `errors.AsType[codedError]`.

### Go 1.26: Green Tea GC
New GC reduces overhead 10-40% in GC-heavy workloads. `wad` benefits automatically during high event throughput.

### Go 1.26: `go fix` rewrite
Rebuilt on the Go analysis framework. Can auto-modernize `errors.As` → `errors.AsType`, older stdlib patterns → newer equivalents. Run `go fix ./...` periodically.

### Go 1.26: `runtime/secret` (experimental)
Secure erasure of sensitive temporaries. Potentially relevant for session store or allowlist parsing where JIDs pass through memory.

### Go 1.26: Goroutine leak profiling (experimental)
`runtime/pprof` goroutineleak profile. Useful for `wad` testing — leaked goroutines from subscribe handlers or event bridges would surface.

---

## 2. Modern Error Handling

### Best practices summary

1. **Sentinel errors** (`var ErrFoo = errors.New("foo")`) for fixed conditions matched with `errors.Is`. Good for domain-level errors like `ErrInvalidJID`.

2. **Custom error types** (structs implementing `error`) when callers need structured data. The `rpcErr` pattern in the codebase is correct. With Go 1.26, use `errors.AsType[*rpcErr](err)`.

3. **Error wrapping** with `fmt.Errorf("context: %w", err)` — single `%w` verb, no mixing with `%v`. The `%w + %v` pattern found across the codebase should be standardized.

4. **Handle errors once.** Don't log AND return. The audit log port correctly separates concerns.

5. **No silent fallbacks.** Already rule 12 in CLAUDE.md.

6. **Wrap at port boundaries.** Secondary adapters translate whatsmeow errors to domain errors with `%w` so the original is inspectable but domain errors match with `errors.Is`.

### Rejected alternative: Error values via generics
Community proposals for `Result[T, E]` types have not gained traction. Go's `(T, error)` convention remains canonical.

---

## 3. Static Analysis & Linting

### Current config assessment
`.golangci.yml` already enables: `depguard`, `errcheck`, `forbidigo`, `gocritic`, `gocyclo`, `gosec`, `govet`, `revive`, `staticcheck`, with `gofumpt` as formatter. This is a strong foundation.

### Tier 1 — Strongly recommended additions (high signal, low noise)

| Linter | What it catches | Why |
|---|---|---|
| **modernize** | Outdated stdlib patterns → modern equivalents | Auto-fixable. New in golangci-lint v2.6.0. Since project targets Go 1.25+, all suggestions apply. |
| **bodyclose** | Unclosed HTTP response bodies | Prevents resource leaks. Preset: `bugs`. |
| **noctx** | HTTP requests without `context.Context` | Critical for timeout/cancellation. Updated v0.5.1 (March 2026). |
| **sqlclosecheck** | Unclosed `database/sql` rows/statements | Directly relevant with SQLite via `modernc.org/sqlite`. Updated v0.6.0 (March 2026). |
| **wrapcheck** | Unwrapped errors from external packages | Enforces `%w` discipline at package boundaries. Tune `ignoreSigs` for stdlib. |
| **musttag** | Missing struct tags on marshaled fields | Catches missing `json:""` / `toml:""` on wire protocol types. v0.14.0 with interface method support. |
| **errorlint** | Non-Go-1.13+ error patterns | Enforces `%w` verb, `errors.Is`/`errors.As` instead of `==`. |

### Tier 2 — Situationally valuable additions

| Linter | What it catches | Notes |
|---|---|---|
| **nilnil** | Simultaneous `nil` error + invalid value returns | Low noise. Catches real bugs. |
| **gocognit** | Cognitive complexity (vs cyclomatic) | Penalizes nesting depth. Threshold ~20 alongside existing gocyclo(15). |
| **goconst** | Repeated string literals | Enable with test file exclusion. Default: 3 occurrences, min length 3. |
| **exhaustive** | Non-exhaustive switch on enum types | Useful but scoped narrowly to `internal/domain/` types to avoid noise. |

### Tier 3 — Skip or defer

| Linter | Why skip |
|---|---|
| **exhaustruct** | Too noisy — Go idiom relies on zero values. Only for specific safety-critical domain types. |
| **prealloc** | Premature optimization. Only for proven hot paths. |
| **NilAway** (Uber) | Not yet ready for CI enforcement. False positives, requires custom binary build. Viable as advisory-only local tool. |

### Formatting
**Keep `gofumpt` as sole formatter.** It is a strict superset of `gofmt` with ~15 opinionated rules. Do not enable both gofumpt and goimports (import handling conflicts). If strict import ordering is needed, add `gci` but test for conflicts first.

### Custom linter rules
The **Module Plugin System** (golangci-lint v2, `custom-gcl.yml`) is the way to write project-specific analyzers. The legacy Go Plugin System requires CGO — **incompatible with this project's CGO_ENABLED=0 invariant**. Existing `depguard` + `forbidigo` already covers the most critical rules.

### govulncheck
**OSV-Scanner V2 replaces govulncheck in CI** (per CLAUDE.md rule 24) because it invokes govulncheck internally. Keep govulncheck for local developer use.

---

## 4. Concurrency Patterns

### errgroup vs oklog/run.Group

**errgroup** (`golang.org/x/sync/errgroup`): Standard for concurrent goroutines that return errors. `WithContext` cancels when any goroutine errors. `SetLimit(n)` caps concurrency. Best for request-scoped fan-out.

**oklog/run.Group**: Actor model for long-running daemons. Each actor is an `(execute, interrupt)` pair. When one exits, all are interrupted. Used by Prometheus.

**Recommendation**: The current manual `sync.WaitGroup` + `context.WithCancel` + reverse-order teardown at `cmd/wad/main.go:225-253` works well. Consider `run.Group` if adding more daemon-level goroutines.

### Context propagation — already correct

The codebase implements dual-context correctly:

| Context Level | Created From | Cancelled When |
|---|---|---|
| Root (signal) | `context.Background()` | SIGTERM/SIGINT |
| Server | Root | Root cancels |
| Connection | `context.Background()` (detached!) | Drain deadline or disconnect |
| Request | Connection | Connection cancels or handler returns |
| whatsmeow client | `context.Background()` | Daemon shutdown (never from request) |

The detached connection context (`connection.go:57-74`) allows in-flight requests to drain during shutdown. This is correct and well-documented.

### Bounded buffer + backpressure — already correct

`Connection.out` is `chan []byte` with capacity 1024. `pushNotification()` does non-blocking send with `default` case triggering disconnect on backpressure ("drop-and-disconnect" strategy, appropriate for unix socket where slow consumer is a bug).

`fanOutEvent()` takes snapshot of connections under lock, then iterates without lock — correct pattern to avoid holding `connsMu` during writes.

### sync.OnceValue / sync.OnceFunc (Go 1.21+)

Limited applicability — daemon uses eager initialization. Could be useful for lazy singletons in library code. Key caveat: panics and errors are cached permanently.

### Graceful shutdown — enhancement opportunity

Current multi-phase shutdown is well-implemented. One enhancement: **double-signal force-kill**:
```go
ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
defer stop()
<-ctx.Done()
stop() // re-register default handler — second signal kills immediately
```

---

## 5. Structured Logging (slog)

### Architecture-specific patterns for wad

1. **Handler per environment**: JSON handler in production (log aggregation), tinted text handler in dev (already using `lmittmann/tint`).

2. **Logger injection via struct field**, not package globals. For hexagonal architecture, inject at construction time:
```go
type WhatsmeowAdapter struct {
    client *whatsmeow.Client
    log    *slog.Logger
}
```

3. **LogValuer for domain types**: Implement `slog.LogValuer` on `domain.JID` and `domain.Message` to control log representation and redact sensitive fields:
```go
func (j JID) LogValue() slog.Value {
    return slog.StringValue(j.String())
}
```

4. **Structured groups for hierarchy**: Use `slog.Group` to namespace adapter-specific fields.

5. **Log levels**: `Debug` for adapter internals, `Info` for lifecycle events, `Warn` for recoverable issues (rate limit, allowlist refusal), `Error` for unrecoverable failures only. Never `Error` for expected business rejections.

---

## 6. Testing Patterns

### testing/synctest expansion opportunities

Already used for rate limiter tests. Expand to:
- EventBridge timeout/backpressure behavior
- Reconnect state machine transitions
- Subscribe/wait timeout logic
- Any test currently using real `time.Sleep` for synchronization

### Fuzz testing — Scorecard credit opportunity

`domain/jid.go` round-trip invariant (line 89: `Parse(j.String()) == j`) is a textbook fuzz target:
```go
func FuzzJIDParse(f *testing.F) {
    f.Add("5511999999999@s.whatsapp.net")
    f.Add("120363123456789@g.us")
    f.Add("+55 11 99999-9999")
    f.Add("")
    f.Fuzz(func(t *testing.T, input string) {
        jid, err := domain.Parse(input)
        if err != nil { return }
        jid2, err := domain.Parse(jid.String())
        if err != nil { t.Fatalf("round-trip failed: %v", err) }
        if jid != jid2 { t.Fatalf("mismatch: %v != %v", jid, jid2) }
    })
}
```
Commit corpus under `testdata/fuzz/FuzzJIDParse/`. Free +10 on Scorecard Fuzzing.

### Table-driven test modernization (Go 1.22+)

- `t.Parallel()` at both top-level and subtest level — no `tt := tt` capture needed from Go 1.22+
- `t.Cleanup()` for teardown instead of `defer` (cleanup runs after subtests complete)
- Check function fields (`check func(t *testing.T, got Result)`) when assertions vary per case
- Each test case self-contained, descriptive `name` that reads as a sentence

### Testable examples as documentation

`ExampleParse`, `ExampleJID_String` in `_test.go` files serve as both living documentation and regression tests. The `// Output:` comment is the assertion. Guarantees documentation never goes stale.

---

## 7. Interface Design

### Current state — already excellent

The ports at `internal/app/ports.go` follow modern best practices:
- Small interfaces: `Allowlist` (1 method), `AuditLog` (1 method), `Pairer` (1 method)
- Defined at consumer site (`internal/app`), not implementation site
- Named by intent ("MessageSender", not "WhatsmeowSender")
- Accept `context.Context` where I/O is involved; omit where pure

### 2025-2026 additions

1. **Use `iter.Seq` for lazy collections at port boundaries.** If a future method returns unbounded data, prefer `iter.Seq[T]` over `[]T`. Caller controls iteration and can break early.

2. **Unexported interfaces for internal contracts.** The `codedError` interface at `errors.go:7` is correctly unexported.

3. **With generics stable (Go 1.18+), prefer type parameters over `any`/`interface{}` in new code.**

---

## 8. Code Readability & Style

### Line-of-sight rule (Dave Cheney)
Happy path hugs the left margin. Guard clauses return early. Maximum indentation depth of 2. Three levels signals refactoring. The codebase mostly follows this — the rate limiter 3-level nesting (M-005) is the exception.

### Guard clauses
- Check-and-return, never check-and-continue-in-else
- **Always return after a guard.** Forgetting to return after logging an error is the #1 Go bug
- The `if err == nil` pattern at `method_send.go:154` (H-008) violates this

### Google vs Uber style guides — key differences

| Topic | Google | Uber |
|---|---|---|
| Mutex embedding | Acceptable | **Do not embed** even on unexported types |
| Nil slices | Use `nil` | Return `nil`, check with `len(s) == 0` |
| Goroutine ownership | Context-based | Every goroutine needs an owning object with `Close`/`Stop` |
| Performance | Readability first | `strconv` over `fmt`, pre-allocate, avoid repeated conversions |
| Serialization tags | Optional | **Mandatory** on marshaled struct fields |

### Functional options — 10-year lessons

1. Required arguments are positional; everything optional
2. Options must be order-independent
3. Set good defaults in constructor — zero-options call must work
4. Unexported config struct, unexported fields
5. Generic variant (Go 1.18+): `type Option[T any] func(*T)`

**When NOT to use**: Fewer than 3 optional parameters, all required, internal/unexported objects.

### Constants and enums — iota patterns

The codebase's `action.go` demonstrates the recommended pattern:
- `iota + 1` so zero is invalid by construction
- Exhaustive `String()` method
- `IsValid()` method
- Bidirectional `ParseAction` for round-trips

**Addition**: Enable the `exhaustive` linter (`nishanths/exhaustive`) to catch non-exhaustive switches on these types.

### Comment conventions — godoc in 2025

Go doc comments use simplified Markdown since Go 1.19. First sentence is summary shown in listings — must start with identifier name. Example functions (`ExampleParse()`) in `_test.go` files are both documentation and regression tests.

---

## 9. Domain-Driven Design in Go

### Value objects (already exemplary)
`JID` in `jid.go`: Unexported fields with constructor validation, value receiver methods, zero value meaningful and safe via `IsZero()`.

### Entities
Pointer receivers for mutation. Unexported fields + exported getters (`ID()` not `GetID()`). All validation in constructor — if constructor succeeds, object is always valid.

### Aggregates
`Allowlist` in `allowlist.go` is an aggregate root — encapsulates policy entries, enforces invariants through `Allows()`. Modifications should be single-transaction with mutex or channel serialization.

### Ports as DDD repositories
`SessionStore` is a repository, `HistoryStore` is a read-model query. The separation is correct. Key insight: **push validation to the write side** (constructors), not to the repository.

### What separates "excellent" from "just working"

1. Line-of-sight code — happy path on the left
2. Interfaces at consumer, not provider
3. Zero values useful or obviously invalid
4. Contract documentation on interfaces (numbered clauses MS1-MS6, ES1-ES6)
5. Exhaustive enum handling
6. `testing/synctest` for concurrency
7. Testable examples as documentation
8. Compile gates (`var _ Interface = (*ConcreteType)(nil)`)
9. Naming describes intent, not mechanics
10. Error wrapping with `%w` and sentinel errors

---

## 10. Rate Limiting

### Current implementation — production-ready

Three-tier token bucket via `golang.org/x/time/rate`:
- Per-second (2/s burst 2), per-minute (0.5/s burst 30), per-day (~0.012/s burst 1000)
- Warmup multiplier: 25% days 1-7, 50% days 8-14, 100% day 15+
- `scaledBurst` ensures burst is never zero (permanent denial prevention)
- Per-recipient daily caps (30/day) and new-recipient caps (15/day)

**Correctly uses `Allow()` (non-blocking)** — reject immediately in hot path, not `Wait()`.

**Rejected alternative**: `uber-go/ratelimit` (leaky bucket) smooths bursts instead of allowing them. Not suitable because WhatsApp anti-automation cares about total volume, not inter-message spacing.

**Minor improvement opportunity**: `AllowFor` checks global limits before per-recipient. If global passes but per-recipient fails, a global token is "wasted." Could use `Reserve()` + cancel, but current approach errs on safety side.

---

## 11. Recommended Linter Additions

Ordered by value-to-noise ratio for this project:

```yaml
# Add to .golangci.yml linters.enable:
linters:
  enable:
    # Existing (keep all)
    - depguard
    - errcheck
    - forbidigo
    - gocritic
    - gocyclo
    - gosec
    - govet
    - revive
    - staticcheck
    # New — Tier 1 (high signal)
    - modernize      # auto-fixable Go modernization (golangci-lint v2.6+)
    - bodyclose       # HTTP response body leak prevention
    - noctx           # enforce context in HTTP requests
    - sqlclosecheck   # DB rows/stmt close verification
    - wrapcheck       # error wrapping at package boundaries
    - errorlint       # Go 1.13+ error patterns
    - musttag         # struct tag enforcement for marshal/unmarshal
    # New — Tier 2 (situational)
    - nilnil          # nil error + invalid value returns
    - gocognit        # cognitive complexity (alongside gocyclo)
    - goconst         # repeated string literals

linters-settings:
  gocognit:
    min-complexity: 20
  goconst:
    min-len: 3
    min-occurrences: 3
    ignore-tests: true
  wrapcheck:
    ignoreSigs:
      - io.EOF
      - context.Canceled
      - context.DeadlineExceeded
```

---

## 12. Actionable Items Summary

### Quick wins (< 30 min each)

| Item | Effort | Impact |
|---|---|---|
| Run `go fix ./...` to auto-modernize patterns | 10 min | Catches stale patterns across all files |
| Modernize `IsCodedError` to `errors.AsType` (Go 1.26) | 5 min | Perf + signals modernity |
| Use `new(value)` for optional pointer fields | Per-occurrence | Cleaner code |
| Add `t.Helper()` to test helpers (M-009) | 5 min | Better test output |
| Extract magic numbers to named constants (H-004 thru H-007) | 15 min | Readability |

### Medium effort (1-4 hours)

| Item | Effort | Impact |
|---|---|---|
| Add recommended linters to `.golangci.yml` | 2 hours | Automated quality enforcement |
| Add `FuzzJIDParse` + `FuzzMessageValidate` fuzz targets | 1 hour | Scorecard credit + real bug finding |
| Implement `slog.LogValuer` on domain types | 30 min | Consistent, safe logging |
| Standardize error wrapping (`%w` only, no `%w + %v`) | 1 hour | Consistency |
| Fix EventBridge deadlock risk (C-001) | 2 hours | Safety |

### Larger refactors (feature-sized)

| Item | Effort | Impact |
|---|---|---|
| Split whatsmeow Adapter god struct (C-002) | 4-8 hours | Testability, SRP |
| Split socket Server god struct (C-003) | 4-8 hours | Testability, SRP |
| Replace `os.Exit` in CLI handlers with error returns (M-021) | 2-4 hours | Testability |
| Extract composition root sub-functions (M-023, M-024) | 2 hours | Readability |
| Add benchmarks for hot paths (L-006, L-025) | 2-4 hours | Performance baseline |

---

## 13. Sources

### Go Language & Features
- [Go 1.25 Release Notes](https://go.dev/doc/go1.25)
- [Go 1.26 Release Notes](https://go.dev/doc/go1.26)
- [Go Blog: Ranging over functions in Go 1.23](https://go.dev/blog/range-functions)
- [Go Blog: Testing concurrent code with synctest](https://go.dev/blog/synctest)
- [Go Blog: Using go fix to modernize Go code](https://go.dev/blog/gofix)
- [errors.AsType — Go 1.26 (antonz.org)](https://antonz.org/accepted/errors-astype/)
- [Eli Bendersky: Ranging over functions in Go 1.23](https://eli.thegreenplace.net/2024/ranging-over-functions-in-go-123/)
- [Applied Go: The Synctest Package](https://appliedgo.net/spotlight/go-1.25-the-synctest-package/)
- [Bitfield Consulting: Iterators in Go](https://bitfieldconsulting.com/posts/iterators)

### Style Guides
- [Google Go Style Guide](https://google.github.io/styleguide/go/guide.html)
- [Google Go Style Decisions](https://google.github.io/styleguide/go/decisions.html)
- [Google Go Best Practices](https://google.github.io/styleguide/go/best-practices.html)
- [Uber Go Style Guide](https://github.com/uber-go/guide/blob/master/style.md)
- [Dave Cheney: Practical Go](https://dave.cheney.net/practical-go/presentations/qcon-china.html)

### Static Analysis
- [golangci-lint Linters docs](https://golangci-lint.run/docs/linters/)
- [golangci-lint v2 announcement](https://ldez.github.io/blog/2025/03/23/golangci-lint-v2/)
- [Golden config for golangci-lint (maratori)](https://gist.github.com/maratori/47a4d00457a92aa426dbd48a18776322)
- [NilAway: Practical Nil Panic Detection (Uber)](https://www.uber.com/us/en/blog/nilaway-practical-nil-panic-detection-for-go/)
- [uber-go/nilaway GitHub](https://github.com/uber-go/nilaway)
- [musttag GitHub](https://github.com/go-simpler/musttag)
- [gocognit GitHub](https://github.com/uudashr/gocognit)
- [exhaustive GitHub](https://github.com/nishanths/exhaustive)

### Concurrency & Architecture
- [Go Blog: Context](https://go.dev/blog/context)
- [Go Blog: Go Concurrency Patterns: Pipelines](https://go.dev/blog/pipelines)
- [VictoriaMetrics: Graceful Shutdown in Go](https://victoriametrics.com/blog/go-graceful-shutdown/)
- [VictoriaMetrics: Go synctest](https://victoriametrics.com/blog/go-synctest/)
- [Gopher Academy: oklog/run.Group](https://blog.gopheracademy.com/advent-2017/run-group/)
- [golang.org/x/sync/errgroup](https://pkg.go.dev/golang.org/x/sync/errgroup)
- [golang.org/x/time/rate](https://pkg.go.dev/golang.org/x/time/rate)

### Testing
- [Go Wiki: TableDrivenTests](https://go.dev/wiki/TableDrivenTests)
- [Go Blog: Testable Examples](https://go.dev/blog/examples)
- [Go Fuzzing docs](https://go.dev/doc/security/fuzz/)
- [FOSDEM 2026: testing/synctest](https://fosdem.org/2026/schedule/event/BDH7G7-go-testing-synctest/)

### DDD & Design
- [ByteSizeGo: 10 Years of Functional Options](https://www.bytesizego.com/blog/10-years-functional-options-golang)
- [Dave Cheney: Functional Options](https://dave.cheney.net/2014/10/17/functional-options-for-friendly-apis)
- [Pkritiotis: DDD Entities in Go](https://pkritiotis.io/ddd-entity-in-go/)
- [Dennis Vis: DDD Value Objects in Go](https://dennisvis.dev/blog/ddd-in-go-value-objects)
- [Grab Engineering: DDD in Go](https://engineering.grab.com/domain-driven-development-in-golang)
- [Dan Peterson: Reducing Go Nesting](https://danp.net/posts/reducing-go-nesting/)
