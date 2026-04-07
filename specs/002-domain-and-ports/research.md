# Research: Domain Types and the Seven Port Interfaces

**Spec**: [`spec.md`](./spec.md) · **Branch**: `002-domain-and-ports` · **Date**: 2026-04-06

This is a small Phase 0. Most of the architectural decisions for feature 002 were already locked in [`CLAUDE.md`](../../CLAUDE.md), [`.specify/memory/constitution.md`](../../.specify/memory/constitution.md), and [`specs/001-research-bootstrap/research.md`](../001-research-bootstrap/research.md). The remaining unknowns are tactical Go-language choices: how to model a sum-typed `Message` in idiomatic Go, how to write a shared contract test suite that any adapter can run against itself, what context conventions the seven ports should use, and how to validate WhatsApp JIDs without depending on the whatsmeow library. Each is resolved below in `Decision / Rationale / Alternatives` form.

## D1 — How to model the `Message` sum type in Go

**Decision**: **Sealed-interface variant pattern.** Declare an exported `Message` interface with one unexported sentinel method `isMessage()`, then declare exported variant structs `TextMessage`, `MediaMessage`, `ReactionMessage`, each implementing the sentinel. Pattern-match in callers via type-switch (`switch m := msg.(type) { case TextMessage: ... }`).

**Rationale**: Go has no native sum types. Of the three idiomatic options, the sealed-interface pattern is the only one that gives compile-time exhaustivity (the unexported sentinel forbids out-of-package implementations) without forcing every caller through reflection or generic type-parameter gymnastics. It is the same pattern used by `golang.org/x/tools/go/ast/inspector` for AST node families and by `cmp.Option` in `google/go-cmp`. Each variant is a plain struct with its own fields, JSON tags work normally, and `Validate()` lives on the variant rather than on a discriminator field. The forbidden-out-of-package property gives us the "exhaustive switch" property the constitution's anti-pattern #2 implicitly wants without an external linter.

**Alternatives considered**:

- **Tagged struct with `Type` discriminator field** (`type Message struct { Type MessageType; Body string; MediaPath string; ... }`). Rejected: every variant pays the memory cost of every other variant's fields, validation has to branch on the discriminator at runtime, and the type system gives no help when a new variant is added.
- **Generics with constraints** (`type Message[T any] struct { Body T }`). Rejected: forces every caller to know the concrete type at compile time, defeats the purpose of a polymorphic event stream, and Go generics in 2026 still cannot express "one of a closed set of types" without a wrapper interface — so the wrapper interface ends up being needed anyway.
- **`encoding/json.RawMessage` with a runtime registry**. Rejected: pushes type errors from compile time to runtime, which is exactly what hexagonal architecture is meant to prevent at the core boundary.

**Sources**: <https://go.dev/blog/error-syntax> (sealed-interface discussion in error context), <https://github.com/google/go-cmp/blob/master/cmp/options.go> (production sealed interface), <https://pkg.go.dev/golang.org/x/tools/go/ast/inspector> (Variant pattern).

## D2 — How to write the shared contract test suite

**Decision**: **`func RunContractSuite(t *testing.T, factory func(*testing.T) Adapter)` exported from `internal/app/porttest/`.** Each port method gets its own `t.Run("MessageSender/Send/happy", func(t *testing.T) { ... })` subtest. The factory pattern (rather than passing an adapter directly) lets the suite reset state between subtests without relying on the adapter to expose a `Reset()` method.

**Rationale**: This is the **Watermill pubsub-test pattern**, the most production-proven shared-contract approach in the Go ecosystem. Watermill's `pubsub.TestPubSub` ships with their `_examples/basic/cqrs/` tree and is reused by every Watermill backend (Kafka, RabbitMQ, NATS, in-memory). The `factory` indirection is the key insight: it gives each subtest a clean adapter instance, which avoids the brittleness of mutable shared state between tests, and it documents that "stateless construction" is itself part of the port contract — any adapter that cannot be created cleanly per-test fails the suite by construction. Database/sql-style packages (`pgx`, `sqlx`) use the same pattern.

**Alternatives considered**:

- **Single suite instance with `Reset()` method on the adapter**. Rejected: forces every adapter to expose a `Reset()` that the production code never uses, leaks test concerns into production interfaces.
- **Build tags + duplicate test files per adapter**. Rejected: defeats the entire point of "shared contract suite" — every change to a port semantic would need to be duplicated across N test files.
- **Cucumber/Gherkin BDD tooling**. Rejected: overkill for a single-maintainer project, adds a Ruby/JS dependency that violates Constitution Principle IV (no extra runtime requirements).

**Sources**: <https://github.com/ThreeDotsLabs/watermill/blob/master/pubsub/tests/test_pubsub.go>, <https://pkg.go.dev/github.com/jackc/pgx/v5>.

## D3 — Context conventions for the seven ports

**Decision**: **Every port method that may block, do I/O, or be cancelled takes `ctx context.Context` as the first parameter, except `Allowlist.Allows(jid, action)` which is pure and synchronous.** The exception is documented inline in `internal/app/ports.go` so callers do not have to read this research file.

**Rationale**: The Go community convention since the proposal-accepted `context` package landed is "first parameter, named `ctx`, never store on a struct, never pass nil" (<https://pkg.go.dev/context#pkg-overview>, <https://go.dev/blog/context>). For ports that wrap real infrastructure (whatsmeow websockets, SQLite writes, JSON-RPC handlers), the context is load-bearing: it carries deadline, cancellation, and request-scoped tracing. For `Allowlist.Allows`, the call is in-memory by design — carrying a context would invite future implementations to do I/O on the policy-decision path, which is exactly what Constitution Principle III's "non-overridable middleware" intent forbids. Making the exception explicit at the type level prevents that drift.

**Alternatives considered**:

- **Context on every port method including Allows**. Rejected: invites slow allowlist implementations and signals the wrong thing at the type level. Allowlist decisions must be sub-microsecond and side-effect-free.
- **Context-free ports across the board, with cancellation propagated via channels**. Rejected: against community convention; channels-as-cancellation is the pre-1.7 idiom that `context` was designed to replace.
- **Context optional via variadic `opts ...Option`**. Rejected: clever, hides the cancellation contract, fails reviewability.

**Sources**: <https://pkg.go.dev/context#pkg-overview>, <https://github.com/golang/go/wiki/CodeReviewComments#contexts>.

## D4 — How to parse and validate a WhatsApp JID without importing whatsmeow

**Decision**: **Hand-rolled `JID` parser in `internal/domain/jid.go`** that accepts either an E.164-ish phone string (with or without `+`, with or without spaces, hyphens, or parentheses) and produces a `<digits>@s.whatsapp.net` value, OR accepts an already-formed `<token>@<server>` string and validates the server suffix. The implementation uses only `unicode`, `strings`, and `errors` from the stdlib.

**Rationale**: FR-009 forbids importing `go.mau.fi/whatsmeow` from any file under `internal/domain` or `internal/app`. whatsmeow's `types.JID` is the obvious source of truth, but using it would put the compile-time enforcement in conflict with the design. Hand-rolling is cheap (~60 lines) and gives us:

1. A `JID` type that is a Go value (not a pointer), so it composes into structs and switch cases without nil-checks.
2. A `Parse(input string) (JID, error)` constructor with a typed error (`ErrInvalidJID` wrapped via `fmt.Errorf("%w: %s", ErrInvalidJID, input)`) so callers can `errors.Is`.
3. A `String()` method matching the canonical `<digits>@s.whatsapp.net` form for round-tripping.
4. `IsUser()` and `IsGroup()` predicates for the discriminating switch.

The whatsmeow secondary adapter in feature 003 will translate `whatsmeow/types.JID ↔ domain.JID` at the boundary using `domain.Parse(types.JID.String())` and `types.JID(domain.JID.String())`. The translation is single-line and lossless.

The valid digit-length range is 8–15 (E.164 minimum/maximum, per ITU-T E.164 recommendation §6.2 — <https://www.itu.int/rec/T-REC-E.164/>), with no upper bound on which country codes are accepted. WhatsApp historically rejects numbers that have never registered, but that is an adapter-level concern, not a domain-level one — the domain validates the *shape*, not the *registration*.

**Alternatives considered**:

- **Reuse whatsmeow's JID type with a build tag**. Rejected: the depguard rule cannot be bypassed by build tags without explicit allowlisting, and the constitution's principle I forbids it categorically.
- **Use a third-party phone-number library** like `nyaruka/phonenumbers` (a Go port of Google's libphonenumber). Rejected: 8 MB of dependencies for a 60-line problem, and libphonenumber's normalisation is more permissive than WhatsApp's actual JID format.
- **Treat JID as an opaque `string` type with a `Validate()` function**. Rejected: less type safety, no compile-time prevention of "passing a contact name where a JID was expected" bugs.

**Sources**: <https://www.itu.int/rec/T-REC-E.164/> (E.164 length spec), <https://pkg.go.dev/errors#Is> (typed error wrapping), <https://github.com/google/libphonenumber> (alternative considered and rejected).

## D5 — Where to draw the boundary between domain and use case

**Decision**: **Domain types own pure validation and value transformation. The `app` package owns orchestration that needs more than one port.** Allowlist policy decision (`Allows(jid, action)`) lives in domain because it is a pure function of state. Rate limiting lives in the app layer because it needs a clock and persistent state. Audit logging lives in the app layer because it depends on the `AuditLog` port.

**Rationale**: This is the textbook hexagonal split (Robert Laszczak's 2024 talk: "use cases coordinate, domain decides"). The constitution's Principle I locks the dependency direction (domain → nothing; app → domain + ports; adapters → app + domain), and this decision keeps the directionality clean. Putting `Allows` in domain rather than in `app` is unusual but justified: the allowlist is a pure data structure, the decision is a pure function of that data structure, and pulling it into `app` would force the rate limiter and audit logger to depend on a use-case wrapper around what should be a single struct method. The future rate limiter middleware in feature 004 will *consume* `Allowlist.Allows` as a building block, not own it.

**Alternatives considered**:

- **All policy in `app` layer, domain is data-only DTOs**. Rejected: produces an anemic domain (constitution anti-pattern #2) and forces every caller to assemble policy from the outside.
- **Policy in `internal/policy/` as a third top-level package**. Rejected: invents an extra namespace for one decision; complexity without payoff.

**Sources**: ThreeDotsLabs Wild Workouts DDD example: <https://github.com/ThreeDotsLabs/wild-workouts-go-ddd-example>, Robert Laszczak's 2024 talk on hexagonal Go.

## Phase 0 outcome

Zero `[NEEDS CLARIFICATION]` markers carried into Phase 1. Five tactical decisions resolved with sources. None of them contradict CLAUDE.md or the constitution; D5 sharpens an existing principle that was implicit before.

## Sources (consolidated)

- <https://github.com/google/go-cmp/blob/master/cmp/options.go>
- <https://pkg.go.dev/golang.org/x/tools/go/ast/inspector>
- <https://github.com/ThreeDotsLabs/watermill/blob/master/pubsub/tests/test_pubsub.go>
- <https://pkg.go.dev/github.com/jackc/pgx/v5>
- <https://pkg.go.dev/context#pkg-overview>
- <https://github.com/golang/go/wiki/CodeReviewComments#contexts>
- <https://www.itu.int/rec/T-REC-E.164/>
- <https://pkg.go.dev/errors#Is>
- <https://github.com/google/libphonenumber>
- <https://github.com/ThreeDotsLabs/wild-workouts-go-ddd-example>
- [`CLAUDE.md`](../../CLAUDE.md) §"Ports", §"Domain types", §"Anti-patterns"
- [`.specify/memory/constitution.md`](../../.specify/memory/constitution.md) Principles I, V, VI
