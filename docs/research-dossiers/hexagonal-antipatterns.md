# Dossier: Anti-Patterns in Go Hexagonal / DDD (2026)

## 1. Cockburn's Original Paper (2005)

Cockburn's "Hexagonal Architecture" (alistaircockburn.com, first drafted 2005, revised through 2008) frames the pattern's intent as: *"Allow an application to equally be driven by users, programs, automated test or batch scripts, and to be developed and tested in isolation from its eventual run-time devices and databases."* The hexagon shape was deliberately **not** meant to imply six sides or six layers; Cockburn explicitly wrote *"The number six is not important... it is a symbol for the drawing, and leaves room to insert ports and adapters as needed."* The paper warns against the pattern being confused with layering: *"The [layered] pattern... did not clearly get across two points: that the developer should deliberately isolate application from infrastructure, and that the asymmetry of left/right, upper/lower is artificial."* Community drift: modern "hex" diagrams routinely label six fixed layers (domain / application / ports / adapters / infra / presentation), which is the opposite of Cockburn's intent. Source: https://alistair.cockburn.us/hexagonal-architecture/

## 2. Vernon — Implementing DDD (IDDD, 2013, still the Go community reference in 2026)

Vernon's aggregate rules (Ch. 10): (a) model true invariants within a consistency boundary, (b) design small aggregates, (c) reference other aggregates by identity, (d) update other aggregates using eventual consistency. He names the **Anemic Domain Model** (quoting Fowler) as *"an anti-pattern... the behavior is in services that sit on top of the domain model."* He names the **"Save the World" Repository** as the repository that fetches entire object graphs to satisfy one use case. **Leaky aggregates** = aggregates that expose internal collections via getters, allowing external mutation. **Double-write** = writing to DB and publishing an event in two separate transactions (use outbox instead).

## 3. Evans — Blue Book (2003)

Evans on model vs schema (Ch. 6, "Repositories"): *"A model is not the database... objects have associations; a database has foreign keys... The model should not be constrained by the persistence mechanism."* On layering (Ch. 4): Evans describes a four-layer model (UI / Application / Domain / Infrastructure) but explicitly says *"Infrastructure... should not make business decisions."* Evans predates hexagonal but the dependency direction is identical — hexagonal generalizes Evans by admitting the left/right symmetry Cockburn pointed out.

## 4. Martin — Clean Architecture (2017)

The Dependency Rule: *"Source code dependencies must point only inward."* Four rings: Entities / Use Cases / Interface Adapters / Frameworks & Drivers. Entities = enterprise-wide business rules; Use Cases = application-specific rules. Overlap with hexagonal: both enforce inward dependency and isolate domain from I/O. Divergence: Clean Architecture mandates the ring topology and a specific use-case interactor shape; hexagonal is agnostic about inner structure — any domain core works as long as ports are the only boundary. In Go, Clean Architecture tends to produce more ceremony (InputBoundary / OutputBoundary / Presenter / ViewModel) than idiomatic hexagonal.

## 5. ThreeDotsLabs / wild-workouts

Repo: https://github.com/ThreeDotsLabs/wild-workouts-go-ddd-example. Laszczak's posts at https://threedots.tech call out: (a) *"The Repository Pattern: Why you should use it"* — argues the repository interface must live in the domain package, not the infra package; (b) *"Clean Architecture in Go"* — warns against anemic structs and pushes "always-valid" domain types with unexported fields plus constructors returning `(T, error)`; (c) *"Robust Applications with SQL"* — warns against ORMs leaking into domain; (d) blocks PRs that put business logic in HTTP handlers, that expose `*sql.DB` to use cases, or that define domain types as plain `struct{}` with exported fields. Their canonical pattern: domain package defines the interface the repo must satisfy, adapters package implements it, and the DI wiring lives in `main.go` or a small `service` package.

## 6. Mat Ryer — GopherCon

"How I write HTTP services" (2018, updated 2022): one `server` struct holding deps, handlers as methods returning `http.HandlerFunc` closures, dependencies passed explicitly. Ryer's "Java-flavored Go" critique (GopherCon UK "Idiomatic Go Tricks"): warns against `FooService` / `FooServiceImpl` pairs, against factories, against interface-per-struct, and against deep package hierarchies. Quote (paraphrased from talks): *"Don't write Java in Go. A struct is fine. You don't need an interface until you have two implementations — and tests don't count if fakes are trivial."*

## 7. Cheney — SOLID Go Design (2016)

https://dave.cheney.net/2016/08/20/solid-go-design. Cheney: *"The bigger the interface, the weaker the abstraction."* Interfaces belong in the **consumer** package, not the producer. "Accept interfaces, return structs" — but the limit is: don't invent interfaces with one method and one implementation just to satisfy a rule. Cheney explicitly deprecates pre-declared interfaces: define the interface where it is used, keep it to one or two methods.

## 8. Bourgon — Go Best Practices, Six Years In (2016)

https://peter.bourgon.org/go-best-practices-2016/. Verdict on layout: **package by dependency, not by kind**. *"Don't put everything in a package called `models` or `controllers`."* Bourgon endorses flat project layouts with domain types at the repo root, and `cmd/` for binaries. He rejects `pkg/`-tree ceremony for small services.

## 9–11. Consolidated Anti-Patterns and Regrets

Production regrets are documented in posts like "Why we moved away from hexagonal" (various Medium / dev.to 2023–2025), the GoTime podcast episodes on "architecture astronauts", and Kat Zien's "How Do You Structure Your Apps" GopherCon talk (compares flat / layered / DDD / hex on one repo and shows hex has the highest file count for the same feature set). Common regret: port explosion forced a rewrite back to flatter layout.

---

## PR-Review Checklist: 13 Concrete Anti-Patterns

1. **DO NOT place port interfaces in the same package as their adapter implementation.** Interfaces belong in the consumer (domain/use case) package. *Cite:* Cheney, SOLID Go Design. *Alternative:* define `type UserRepo interface{...}` in the domain package; implement in `adapters/postgres`.

2. **DO NOT declare an interface before a second implementation or a real test double exists.** *Cite:* Ryer, GopherCon; Cheney. *Alternative:* start with a concrete struct; extract interface at the seam only when mocked or swapped.

3. **DO NOT write repositories with one method per SQL query (`GetUserByEmail`, `GetUserByID`, `GetUserByPhone`...).** That is CRUD in DDD costume. *Cite:* Vernon IDDD Ch. 12 ("Repositories"). *Alternative:* repository methods correspond to aggregate lifecycle (`Save`, `ByID`, `Remove`) plus specifications/queries for read models via CQRS.

4. **DO NOT expose exported mutable fields on aggregates or value objects.** Leaky aggregate. *Cite:* Vernon IDDD Ch. 10. *Alternative:* unexported fields, constructor `NewX(...) (X, error)`, behavior methods returning new values.

5. **DO NOT put business rules in HTTP handlers or in gRPC servers.** *Cite:* ThreeDotsLabs "Clean Architecture in Go". *Alternative:* handler unmarshals, calls a use-case function, marshals response.

6. **DO NOT write to the DB and publish an event in two separate transactions.** Double-write. *Cite:* Vernon IDDD Ch. 8. *Alternative:* transactional outbox.

7. **DO NOT model anemic domain structs with `Get`/`Set` pairs and all logic in a `Service`.** *Cite:* Fowler via Vernon IDDD Ch. 1; ThreeDotsLabs. *Alternative:* behavior on the aggregate; services coordinate across aggregates only.

8. **DO NOT introduce a DTO at every layer boundary.** *Cite:* Bourgon, Go Best Practices. *Alternative:* one DTO at the transport edge; domain types cross internal boundaries.

9. **DO NOT proliferate `FooService` / `FooManager` / `FooHandler` / `FooInteractor` for the same concept.** *Cite:* Ryer GopherCon; Cockburn on layer asymmetry. *Alternative:* one name per concept, chosen from ubiquitous language.

10. **DO NOT fix the number of hexagon "layers" at six (or seven onion rings).** *Cite:* Cockburn original paper ("the number six is not important"). *Alternative:* ports added as driven/driving needs emerge.

11. **DO NOT use a DI container (wire is fine; reflective containers are not) in lieu of explicit `main.go` wiring.** *Cite:* Bourgon; Ryer. *Alternative:* construct dependencies top-down in `main`, pass explicitly.

12. **DO NOT write tests that mock every collaborator and assert call order.** Couples tests to structure, not behavior. *Cite:* ThreeDotsLabs "Tests in Go"; Vernon IDDD Ch. 4 on test strategy. *Alternative:* in-memory adapter implementing the port; assert on observable state.

13. **DO NOT create a "use case" that is a one-line delegation to a repository method.** It is a port in disguise. *Cite:* Martin, Clean Architecture Ch. 20 ("Use Cases"). *Alternative:* collapse the use case until it does real orchestration, or call the repo directly from the handler and add a use case when logic accretes.

### Port-set coverage criteria

A port set is **complete** when every external interaction (inbound driver or outbound driven) traverses exactly one port and the domain package imports zero infra packages (`go list -deps` proves it). **Over-decomposed** when ports have one method each, one implementation each, and one caller each — collapse them. **Under-decomposed** when a single port mixes unrelated capabilities (e.g., `Storage` doing users + billing + audit) — split along bounded-context lines. Literature anchor: Vernon IDDD Ch. 2 (Bounded Contexts) + Cockburn's original "ports are conversations, not CRUD".

### Primary sources

- https://alistair.cockburn.us/hexagonal-architecture/
- Vernon, *Implementing Domain-Driven Design*, Addison-Wesley 2013
- Evans, *Domain-Driven Design*, Addison-Wesley 2003
- Martin, *Clean Architecture*, Prentice Hall 2017
- https://threedots.tech and https://github.com/ThreeDotsLabs/wild-workouts-go-ddd-example
- https://dave.cheney.net/2016/08/20/solid-go-design
- https://peter.bourgon.org/go-best-practices-2016/
- Mat Ryer, "How I write HTTP services after eight years" (pace.dev, 2020)
- Kat Zien, "How Do You Structure Your Go Apps" GopherCon 2018
