# Dossier: Measuring Architectural Decision & Specification Quality (2026)

## 1. ATAM and ARID (SEI/CMU)

The Architecture Tradeoff Analysis Method was formalized in Kazman, Klein & Clements, *ATAM: Method for Architecture Evaluation* (CMU/SEI-2000-TR-004), building on Kazman, Bass, Abowd & Webb's 1994 SAAM paper and the 1998 ICSE paper "The Architecture Tradeoff Analysis Method." ATAM evaluates a design against the ISO/IEC 25010 quality attributes — primarily **performance, availability, security, modifiability, usability, testability, and interoperability** — by eliciting *quality attribute scenarios* (stimulus, environment, artifact, response, response measure) and mapping each scenario onto architectural decisions, then labeling them as **sensitivity points, tradeoff points, risks, or non-risks**.

Full ATAM assumes 2 phases, ~30 people, and weeks of effort — overkill for a small spec. The SEI's scaled-down variant is **ARID — Active Reviews of Intermediate Designs** (Clements, 2000, CMU/SEI; later in *Evaluating Software Architectures*, Clements/Kazman/Klein 2002, ch. 10). ARID combines Parnas's *active design reviews* with ATAM's scenario brainstorming: reviewers are handed the partial design and must *use* it to write pseudo-code solving seeded scenarios. If they cannot, the spec is under-specified. For a solo-dev spec review, ARID collapses to: "pick 5 scenarios, try to implement each against only what the spec says, note every gap." That is the minimum viable ATAM.

## 2. ADRs (Nygard, 2011)

Michael Nygard, "Documenting Architecture Decisions" (thinkrelevance.com, 15 Nov 2011), proposed the four-section template: **Title, Status (proposed/accepted/deprecated/superseded), Context, Decision, Consequences**. An ADR is *sufficient* when the decision is (a) architecturally significant (hard to reverse, cross-cutting), (b) has a non-obvious rationale, and (c) records forces in tension. It is *insufficient* when it only states what was done without the alternatives rejected and their tradeoffs — this is the point reinforced by the MADR extension (markdownadr.github.io) which adds "Considered Options" and "Pros/Cons" sections.

Speckit's `research.md` "Decision / Rationale / Alternatives considered" blocks are effectively MADR-lite ADRs inlined into the research artifact. The quality test is identical to Nygard's: each block must name at least one rejected alternative with its reason-for-rejection, or it is a decision log, not a decision record.

## 3. C4 Model (Simon Brown)

Simon Brown's C4 model (c4model.com; *The Art of Visualising Software Architecture*, Leanpub 2018) defines four levels: **System Context, Container, Component, Code**. Brown's explicit minimum is the **top two diagrams** (Context + Containers). Level 3 (Component) is optional, level 4 (Code) almost never worth drawing. Brown's "what the tests are for a diagram" heuristic: every box must have a defined responsibility, every line must have a direction and a protocol/technology label, and nothing on the diagram may be ambiguous about "is this a process, library, or data store?"

Mapping to speckit: `spec.md` corresponds to the Context level (actors, external systems, user goals), `plan.md` + `contracts/` to Container/Component (processes, APIs, adapters), `quickstart.md` is the runtime walkthrough Brown recommends to supplement static diagrams, and `research.md` holds the decisions those diagrams embody. C4's missing piece in speckit is an explicit **diagram artifact** — Brown argues text alone under-specifies topology.

## 4. Keeling — *Design It!* (Pragmatic, 2017)

Michael Keeling's book popularizes **risk-driven architecture** (originally George Fairbanks, *Just Enough Software Architecture*, 2010): the amount of design you do is proportional to the engineering risk. Key artifacts:
- **Architecture Haiku** — one page: goals, constraints, principles, key decisions. Keeling's argument is that if it does not fit on a page, the team has not decided yet.
- **Minimum Viable Architecture (MVA)** — the smallest set of decisions that lets construction start without forcing rework when the *known* risks materialize.
- **Risk Storming** — a collaborative technique where each participant marks diagrams with colored dots indicating perceived risk hotspots; clusters signal where design effort is needed.

Quality test per Keeling: "For every top risk, is there a decision in the spec that addresses it?" If a risk has no corresponding decision, the spec is incomplete.

## 5. Hohpe — *The Software Architect Elevator* (O'Reilly, 2020)

Gregor Hohpe frames architects as **elevator riders** between the penthouse (strategy) and engine room (code), whose job is translation. Architecture documents are judged by whether they **"make the important decisions visible and the unimportant ones invisible"** (ch. 9, "Decisions"). Hohpe's quality criteria for architectural writing: (a) it must state the decision *and* the option space, (b) it must make assumptions falsifiable, (c) it must be **boring** — clear beats clever — and (d) it must survive being read by someone two levels up or down the org. He explicitly endorses ADRs and warns against "selling architecture" documents that advocate rather than analyze.

## 6. Ousterhout — *A Philosophy of Software Design* (2nd ed., 2021)

Architectural lens only. Core heuristics:
- **Deep modules** (ch. 4): large functionality behind a small interface. Interface complexity / implementation complexity is the ratio to maximize. A port with 30 methods guarding 40 methods of logic is *shallow* and thus bad.
- **Information hiding** (ch. 5): the decision-quality test is "what does a caller need to *not* know?"
- **Different errors are really the same** (ch. 10): exception taxonomies that distinguish cases callers handle identically are a design smell; collapse them.
- **Define errors out of existence** (ch. 10): the strongest API is the one where the error cannot be expressed.

## 7. Cockburn — Hexagonal Architecture (2005)

Alistair Cockburn, "Hexagonal Architecture" (alistair.cockburn.us, Jan 2005; revised 2008). Original motivation: escape the asymmetry between "UI" and "database" by treating **both as just adapters**. Cockburn's definition of a port: **"an intent of conversation"**, a purpose-shaped boundary — not a technology shape.

- **Bad port**: named after infrastructure (`DatabasePort`, `KafkaPort`) or a CRUD mirror of a table. This is the *leaky* port.
- **Good port**: named after the *application's conversation* (`ForObtainingRates`, `ForNotifyingCustomers` in the "for-driving / for-driven" convention). The number of ports equals the number of distinct *reasons to talk to the outside world*, not the number of external systems.
- **Completeness test**: every use case in the application must be expressible using only the port set, and every port must be used by at least one use case. Over-decomposition shows as ports with a single method used once; under-decomposition shows as a port whose methods have unrelated callers.

## 8. Empirical Quality Predictors (2020–2026)

- Arcelli Fontana et al., "Arcan: a Tool for Architectural Smells Detection" (ICSA 2017; extended JSS 2021) — the canonical catalog of **cyclic dependency, hub-like dependency, unstable dependency, god component**, empirically correlated with defect density.
- Lenarduzzi et al., "A Systematic Literature Review on Technical Debt Prioritization" (JSS 2021) and Martini et al. (ESEM 2020) link **architectural smells** to 2–4× higher change-proneness vs non-smelly modules.
- Herbold et al., ICSE 2022, show that files touched by code crossing architectural-layer boundaries have ~1.7× the defect rate of same-layer changes.
- SonarQube's architectural rules (squid: `ArchitecturalConstraint`, `CycleBetweenPackages`) and **ArchUnit** (Java) / **ts-arch** / **import-linter** (Python) are the 2026 industry surface for enforcing these as CI gates.
- Konersmann et al., "Evaluation Methods and Replicability of Software Architecture Research" (ICSA 2022) — a sobering meta-review showing most architecture-quality claims lack replication; treat single-paper effect sizes skeptically.

## 9. Anti-patterns in Architectural Specs

| Anti-pattern | Source | Signal |
|---|---|---|
| Too-clever port | Cockburn 2005; Ousterhout ch. 4 | Shallow interface, single caller |
| Kitchen-sink port | Cockburn 2005 | Unrelated methods, multiple unrelated callers |
| Anemic domain | Fowler, "AnemicDomainModel" 2003 | Entities are pure data, logic lives in services |
| Leaky abstraction | Spolsky 2002; Vernon *IDDD* 2013 | Infra types (SQL rows, HTTP req) appear in core |
| Missing observability | Majors/Fong-Jones/Miranda, *Observability Engineering* 2022 | No SLOs, no trace spans, no log schema in spec |
| Untested invariants | Evans *DDD* 2003; Hillel Wayne *Practical TLA+* 2018 | Invariants stated in prose, not in tests or types |
| Unwritten failure modes | Nygard *Release It!* 2nd ed. 2018 | No timeouts, retries, bulkheads, circuit breakers documented |
| Happy-path-only spec | ATAM scenario catalog | Zero "failure/edge" quality scenarios |

## 10. Scoring Rubric (SEI-style lightweight review)

Adapted from the ARID readiness checklist and Bass/Clements/Kazman *Software Architecture in Practice* (4th ed., 2021) appendix B. Score 1 point per "yes", interpret: 0–8 rework, 9–14 acceptable, 15–18 strong.

---

## Hexagonal Spec Quality Heuristics (drop into `/speckit:checklist`)

1. Does every port name describe an *intent of conversation* rather than a technology or external system? (Cockburn, *Hexagonal Architecture*, 2005)
2. Is every port used by at least one use case, and is every use case expressible using only the declared ports? (Cockburn 2005, completeness test)
3. For each port, is the interface complexity strictly smaller than the implementation complexity it hides (deep-module ratio)? (Ousterhout, *APoSD* 2nd ed., ch. 4)
4. Does every architecturally significant decision in `research.md` name at least one rejected alternative with its reason? (Nygard, "Documenting Architecture Decisions", 2011; MADR)
5. Are the top three engineering risks each addressed by a specific decision in the spec? (Keeling, *Design It!* 2017; Fairbanks 2010, risk-driven design)
6. Does the spec contain at least one quality-attribute scenario for each of performance, availability, security, and modifiability, with a measurable response? (Kazman/Bass/Klein, ATAM 2000; ISO/IEC 25010)
7. Could a reviewer hand-simulate three seeded scenarios using only the spec, without asking the author questions? (Clements, ARID 2000)
8. Are all core domain types free of infrastructure leakage — no SQL, HTTP, framework, or serialization types in port signatures? (Vernon, *IDDD* 2013; Spolsky "Leaky Abstractions" 2002)
9. Does the spec define at least one domain invariant as a testable assertion rather than prose? (Evans, *DDD* 2003; Wayne, *Practical TLA+* 2018)
10. Is there a documented failure-mode section covering timeouts, retries, and at least one bulkhead or circuit-breaker boundary? (Nygard, *Release It!* 2nd ed., 2018)
11. Does the spec declare an observability contract — SLOs, key spans, log schema, error taxonomy — for each adapter? (Majors et al., *Observability Engineering*, 2022)
12. Is there a Context-level (C4 L1) and Container-level (C4 L2) view, either diagrammed or textually equivalent, with every arrow labeled by protocol and direction? (Brown, *C4 Model*, c4model.com)
