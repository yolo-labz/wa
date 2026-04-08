# Architecture Decision Quality Checklist: 002-domain-and-ports

**Purpose**: Unit-test the **architectural and design-pattern decisions** documented across [`spec.md`](../spec.md), [`plan.md`](../plan.md), [`research.md`](../research.md), [`data-model.md`](../data-model.md), [`contracts/ports.md`](../contracts/ports.md), and [`contracts/domain.md`](../contracts/domain.md). This is a "unit test for English" pass over the *design quality* of the spec, independent of `requirements.md` (which validates spec hygiene and deliverable presence).

**Created**: 2026-04-06
**Feature**: feature 002 — domain types and the seven port interfaces
**Theme**: design patterns, hexagonal layering, sum-type encoding, port set completeness, anti-pattern coverage, constitution alignment
**Depth**: deep ambiguity hunt
**Audience**: an external maintainer evaluating whether to start `/speckit:implement` against this spec

> Each item asks whether the design choices are *defended* in the spec, not whether the implementation exists. References use `[spec FR-NNN]`, `[plan §X]`, `[research §Dn]`, `[data-model §Y]`, `[contracts/ports §Z]`, `[contracts/domain §W]`, `[CLAUDE.md §V]`, `[constitution §K]`, or quality markers `[Gap]`, `[Ambiguity]`, `[Conflict]`, `[Assumption]`.

## Sum-type encoding (the `Message` and `Event` interfaces)

- [x] CHK001 Is the sealed-interface variant pattern named explicitly as the chosen encoding, and are the rejected alternatives (tagged struct, generics, raw `interface{}`) listed with rejection reasons? [Clarity] [`research §D1`]
- [x] CHK002 Does the spec name how a future variant addition (e.g. `StatusMessage`, `EditMessage`, `PollMessage`) preserves exhaustivity across every existing switch site? [Coverage] [`data-model §Message`] [Gap]
- [x] CHK003 Is the `golangci-lint --enable=exhaustive` check named as the enforcement mechanism for exhaustive switches on `Message`, `Event`, `Action`, `ReceiptStatus`, `ConnectionState`, `PairingState`? [Measurability] [`data-model §Action`] [`data-model §Event`]
- [x] CHK004 Does `data-model.md` document the unexported sentinel method (`isMessage()`, `isEvent()`) as the seal mechanism *and* the constraint that out-of-package types cannot satisfy the interface? [Completeness] [`contracts/domain §message.go`]
- [x] CHK005 Is the `Event` variant set ({`MessageEvent`, `ReceiptEvent`, `ConnectionEvent`, `PairingEvent`}) defended as complete for v0, with a list of explicitly deferred variants (group membership, contact sync, blocked-list change)? [Coverage] [`data-model §event.go`] [Gap]

## Port set completeness and decomposition

- [x] CHK006 Does the spec defend the **count** of seven ports rather than asserting it? Is there evidence that adding an eighth would not be more useful? [Completeness] [`contracts/ports.md` §"Mapping to JSON-RPC methods"]
- [x] CHK007 Does every port name a single capability the application core needs, with no two ports overlapping in scope? [Consistency] [`contracts/ports §1..§7`]
- [x] CHK008 Is the 11-RPC-method-to-7-port mapping in `contracts/ports.md` complete (every JSON-RPC method has at least one port; no port is unused)? [Coverage] [`contracts/ports.md` §"Mapping to JSON-RPC methods"]
- [x] CHK009 Is `Allowlist` defended as a port despite being implemented by the domain `*Allowlist` value type itself, rather than being silently inlined into use cases? [Clarity] [`research §D5`] [`contracts/ports §6`]
- [x] CHK010 Does the spec explain why `EventStream` is a separate port from `MessageSender` rather than a single bidirectional `Channel` port? [Clarity] [`research §D3`] [Gap]
- [x] CHK011 Is the absence of a `Clock` port (or its presence as part of one of the seven) explicitly resolved? `data-model.md` mentions an "injectable clock" for the in-memory adapter but `ports.md` does not list a `Clock` interface. [Ambiguity] [`data-model §session.go`] [`data-model §audit.go`] [`contracts/ports`]
- [x] CHK012 Does the spec name **what would have to change** if the future Cloud-API adapter cannot satisfy `EventStream.Next` semantics (e.g. webhook-only)? [Edge Cases] [`research §D3`] [Gap]
- [x] CHK013 Is the constraint "no port returns `interface{}` or `any`" stated as a hard rule, with the rationale (sum types in domain instead)? [Clarity] [`contracts/ports.md` §"Forbidden patterns"]

## Hexagonal layering and dependency direction

- [x] CHK014 Does the spec state the dependency direction (`domain → ∅`, `app → domain`, `adapters → app + domain`) explicitly, not just by example? [Clarity] [`plan §"Constitution Check"`]
- [x] CHK015 Is the `core-no-whatsmeow` `depguard` rule referenced **by rule name** in the spec, the plan, AND the constitution, so a future drift in any one document is detectable? [Consistency] [`spec FR-009`] [`plan §"Technical Context"`] [`constitution §I`]
- [x] CHK016 Does the spec name the consequence of a `depguard` rule failure as a **CI build failure**, not a soft warning? [Clarity] [`spec FR-009`]
- [x] CHK017 Is the `internal/app/porttest/` package's import boundary defined? Specifically: may it import `internal/adapters/secondary/memory/` for fixture purposes, or must the factory pattern indirect through a constructor function the consumer provides? [Ambiguity] [`research §D2`] [`plan §"Project Structure"`]
- [x] CHK018 Does `data-model.md` enumerate the complete set of stdlib packages the domain may import (`errors`, `fmt`, `strings`, `unicode`, `sync`, `time`) and forbid the rest by stating the closed list? [Completeness] [`contracts/domain §"Universal rules"`]

## Domain type design (anti-anemic + invariant placement)

- [x] CHK019 Does the spec require every exported domain type to have at least one method beyond accessors, and does it name the enforcement mechanism (PR review)? [Measurability] [`spec FR-002`] [`contracts/domain §"Universal rules"`]
- [x] CHK020 Are the exact validation rules per domain type listed in one place, with a `where enforced` column, so a reviewer can find every invariant without grepping? [Completeness] [`data-model §"Invariants and where they live"`]
- [x] CHK021 Is the `MediaMessage` size-check delegation to the adapter explicitly justified, given that the size limit (`MaxMediaBytes`) is declared in the domain? Is this a layer leak or a clean separation? [Consistency] [`contracts/domain §message.go`] [Ambiguity]
- [x] CHK022 Does the spec explain why `Allowlist` is `*Allowlist` (pointer with mutex) while every other domain type is a value? Is the asymmetry called out and defended? [Clarity] [`data-model §allowlist.go`] [Ambiguity]
- [x] CHK023 Is the `MessageID`/`EventID` cross-type assignment prevention property tested? `data-model.md` mentions "named type, not alias" but no test asserts the compiler-error property. [Coverage] [`data-model §ids.go`] [Gap]
- [x] CHK024 Does the spec defend the choice of `iota + 1` (zero is invalid) over `iota` (zero is the first value) for every enum (`Action`, `ReceiptStatus`, `ConnectionState`, `PairingState`, `AuditAction`)? [Clarity] [`data-model §action.go`]
- [x] CHK025 Is the `AuditEvent.NewAuditEvent` direct call to `time.Now()` documented as the **single sanctioned exception** to the "no time except via injectable clock" rule, with the inconsistency explicitly justified? [Consistency] [`contracts/domain §audit.go`]

## Error handling discipline

- [x] CHK026 Does the spec require every domain error to wrap one of six sentinel errors via `fmt.Errorf("%w: %s", ErrXxx, detail)` so callers can `errors.Is`, and does it forbid naked `errors.New` for non-sentinel errors? [Clarity] [`contracts/domain §errors.go`]
- [x] CHK027 Are the error categories returned by each port method enumerated in the contract (e.g. `MessageSender.Send` returns one of `ErrInvalidJID`, `ErrMessageTooLarge`, `ErrEmptyBody`, infrastructure)? [Completeness] [`contracts/ports §1`]
- [x] CHK028 Does the spec forbid the `panic()` antipattern outside `package main` (relying on `forbidigo` to enforce) and forbid `fmt.Print*` outside `cmd/`? [Consistency] [`contracts/ports.md` §"Forbidden patterns"]

## Test strategy and contract suite design

- [x] CHK029 Does the spec explain why the `RunContractSuite(t, factory func)` pattern was chosen over `TestPubSub(t, sut)` (single-instance) or per-adapter duplicated test files? [Clarity] [`research §D2`]
- [x] CHK030 Does the contract suite specify the **observable failure mode** of an adapter that violates a contract clause (e.g. typed assertion failure with the offending method named)? [Measurability] [`spec US4 acceptance scenario 3`] [Gap]
- [x] CHK031 Is the test count target (`~76 domain + ~30 port = ~106`) defended by enumerating which tests cover which invariant, or is it asserted? [Completeness] [`contracts/domain §"Test counts"`]
- [x] CHK032 Is the "tests pass in under 5 seconds" claim (SC-002) defended with measurement, or is it a guess? [Measurability] [`spec SC-002`] [Assumption]
- [x] CHK033 Does the spec require the contract suite to be invokable from outside the `porttest` package by a feature 003 consumer, and is that property tested in this feature's quickstart? [Coverage] [`quickstart.md §6`] [`spec US4 acceptance scenario 1`]

## Naming, conventions, and Go idioms

- [x] CHK034 Do the seven port interface names follow the Go community convention (single verb-noun, no `I` prefix, no `able` suffix unless it's `er`)? [Consistency] [`contracts/ports §1..§7`]
- [x] CHK035 Is `AuditAction` named distinctly enough from `Action` (the policy action type) to prevent at-call-site confusion? Both end in "Action" — would a more defensive name help? [Ambiguity] [`data-model §action.go`] [`data-model §audit.go`]
- [x] CHK036 Does the spec require `ctx context.Context` as the first parameter on every port method that may block or do I/O, with the `Allowlist.Allows` exception explicitly named? [Consistency] [`research §D3`] [`contracts/ports §6`]

## Constitution alignment

- [x] CHK037 Is every constitution principle (I–VII) evaluated in `plan.md` §"Constitution Check" with PASS / N/A / PARTIAL plus evidence? [Coverage] [`plan §"Constitution Check"`]
- [x] CHK038 Is the Principle III "PARTIAL — JUSTIFIED" verdict (Allowlist data here, rate limiter in feature 004) defended as **correct hexagonal layering** rather than as a postponement? [Clarity] [`plan §"Constitution Check"`]
- [x] CHK039 Does the spec assert that any reordering of "Allowlist data lands here, rate limiter lands in feature 004" (e.g. moving the rate limiter into this feature) would require a constitution amendment, or is the boundary informal? [Consistency] [Gap]

## Anti-patterns explicitly avoided

- [x] CHK040 Does the spec name at least five Go-flavored hexagonal anti-patterns this feature avoids (anemic domain, leaky abstraction, primitive obsession, mock-everything tests, premature interface explosion, Java-flavored layering, etc.) with citation back to the source that named the anti-pattern? [Completeness] [`CLAUDE.md §"Anti-patterns"`]
- [x] CHK041 Is the absence of a "Repository" abstraction explicitly defended? Hexagonal in Java/.NET typically introduces a `Repository` interface; this spec uses `SessionStore`, `ContactDirectory`, etc. — is the naming choice justified? [Clarity] [`contracts/ports §3..§5`] [Gap]

## Cross-document consistency

- [x] CHK042 Does the constitution actually mandate "exactly seven ports" as the spec's edge case claims, or is the constitution silent on the count? Verify by reading `.specify/memory/constitution.md` §I. [Conflict] [`spec §"Edge Cases"`] [`constitution §I`]
- [x] CHK043 Does the directory tree in `plan.md §"Project Structure"` exactly match the file list in `data-model.md` (~12 files) and the additions in `contracts/ports.md` (porttest 8 files) and the in-memory adapter (3 files)? Counts align? [Consistency] [`plan §"Project Structure"`] [`data-model §"Package layout"`]
- [x] CHK044 The plan claims "~600 lines of Go across ~12 files" but the file enumeration in plan + data-model + contracts adds to ~22 files. Is this an estimation error? [Conflict] [`plan §"Technical Context"`] [`plan §"Project Structure"`]
- [x] CHK045 Are all 17 functional requirements (FR-001..FR-017) traceable to at least one user story AND at least one entry in either `data-model.md` or `contracts/`? [Coverage] [`spec §"Functional Requirements"`]

## Notes

- This checklist is independent from `requirements.md` (which validates spec hygiene and deliverable presence). Architecture.md validates *design quality* and is intended to be re-run after any commit that touches `spec.md`, `plan.md`, `research.md`, `data-model.md`, or `contracts/`.
- A failing item is a documentation defect, not a code defect. Items that turn into "this is fine, the spec is silent on purpose" should be edited to add an explicit note in the spec — silence is not defence.
- Items marked `[Gap]` are missing requirements or undefended choices. Items marked `[Ambiguity]` are present but unclear. Items marked `[Conflict]` are present but contradicted by another document. Items marked `[Assumption]` are claims not yet supported by evidence.
- Soft cap: 45 items. The 45 here cover the ten most important quality dimensions. Adding more would dilute the signal.
- Re-run after `/speckit:tasks` and again after `/speckit:implement` to confirm that implementation matches the design rather than drifting from it.
