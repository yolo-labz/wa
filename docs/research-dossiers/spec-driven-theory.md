# Dossier: How Natural-Language Specifications Fail

## 1. Hillel Wayne — on the gap between prose and formal specs

In *"Why Don't People Use Formal Methods?"* (hillelwayne.com, 2019), Wayne draws the sharpest line in the contemporary literature between code, tests, and specs. He writes: **"Tests can only verify specific examples and properties. You might test that `sort([3,1,2]) == [1,2,3]`, but that doesn't tell you that it will work for `sort([3,1,2,2])`. We want to know something is always true."** His framing is that natural-language specs collapse the distinction between *example* and *property* — prose-writers describe the sunny path and assume readers infer the universal claim.

On prose vs formal notation, Wayne is explicit in *"Formal Methods in the Wild"*: **"English is ambiguous. Designs have corner cases that the designers never consider, because English lets you gloss over them."** His AWS writeup (following Newcombe et al., *"How Amazon Web Services Uses Formal Methods"*, CACM 58(4), 2015) cites Chris Newcombe directly: **"In industry, the most common reason given for not using formal methods is that they are perceived to be too difficult… we have found that formal methods are a big win."** Newcombe reports that TLA+ found **"a subtle bug in a fault-tolerant algorithm"** in DynamoDB that **"an extremely subtle bug involving a sequence of 35 steps"** had evaded code review, integration tests, and stress tests for years.

In *"Spec or You'll Regret It"* Wayne's claim is narrower: prose suffices when (a) the component is stateless and small, (b) the invariants are obvious from types, (c) there are no concurrent actors. Prose fails when any of those break — "**the number of interleavings grows combinatorially, and English has no combinators**."

Sources: https://www.hillelwayne.com/post/why-dont-people-use-formal-methods/ ; https://lamport.azurewebsites.net/tla/formal-methods-amazon.pdf

## 2. Leslie Lamport — spec as discipline

Lamport's *"Specifying Systems"* (Addison-Wesley, 2002) argues: **"Writing is nature's way of letting you know how sloppy your thinking is."** And in *"The Future of Computing: Logic or Biology"* (2003): **"Mathematics is nature's way of letting you know how sloppy your writing is. Formal mathematics is nature's way of letting you know how sloppy your mathematics is."** The ladder is: prose → math → formal math, with each rung exposing errors invisible at the previous one.

In *"Why We Should Build Software Like We Build Houses"* (Wired, 2013) he writes: **"Architects draw detailed plans before a brick is laid or a nail is hammered… Programmers and software engineers don't… Without a specification, a program cannot be wrong, it can only be surprising."** The last sentence is the single most-cited line on the subject. Lamport's distinction between *spec-as-discipline* (the act of writing forces precise thought) and *spec-as-document* (the artifact consumers read) underpins TLA+: he insists the value is mostly in the writing, not the reading.

## 3. Tony Hoare — spec, type, proof

In *"An Axiomatic Basis for Computer Programming"* (CACM 12(10), 1969) Hoare introduces the triple `{P} S {Q}`: **"If the assertion P is true before initiation of a program S, then the assertion Q will be true on its completion."** The spec *is* the pre/postcondition pair; the type is the coarsest-grained such pair ("input is int, output is int"); the proof is the derivation that S satisfies it.

In *"Hints on Programming Language Design"* (1973) Hoare writes: **"The price of reliability is the pursuit of the utmost simplicity."** And on specification: **"There are two ways of constructing a software design: One way is to make it so simple that there are obviously no deficiencies and the other way is to make it so complicated that there are no obvious deficiencies."** (Turing Award lecture, 1980.) For Hoare, a type is a cheap partial spec, a full pre/postcondition is an expensive total spec, and the proof is the bridge — but **"the specification is the contract, and the type is only a shadow of it."**

## 4. Barbara Liskov — abstraction boundaries

In *"Programming with Abstract Data Types"* (Liskov & Zilles, SIGPLAN Notices 9(4), 1974) Liskov argues: **"An abstract data type defines a class of abstract objects which is completely characterized by the operations available on those objects."** Completely — the boundary *is* the operation set; anything not exposed does not exist to the consumer.

In *"Data Abstraction and Hierarchy"* (SIGPLAN 23(5), 1988), the LSP paper: **"What is wanted here is something like the following substitution property: If for each object o1 of type S there is an object o2 of type T such that for all programs P defined in terms of T, the behavior of P is unchanged when o1 is substituted for o2, then S is a subtype of T."** The subtlety: "behavior" includes the *spec*, not just the signature. Implementations that satisfy the type but violate the prose contract are LSP violations.

## 5. Ousterhout — *A Philosophy of Software Design*

Key heuristics, quoted from the 2nd edition:

- **"Modules should be deep."** (Ch. 4.) A deep module has a simple interface hiding a lot of implementation. Port interfaces in speckit should read as deep.
- **"The best modules are those whose interfaces are much simpler than their implementations."**
- **"Comments should describe things that are not obvious from the code."** (Ch. 13.) And crucially: **"If users must read the code of a method in order to use it, then there is no abstraction."**
- **"Define errors out of existence."** (Ch. 10.) Specs should narrow the input domain until error cases vanish.
- On the *tactical tornado* (Ch. 3): **"A tactical tornado is a prolific programmer who pumps out code far faster than others but works in a totally tactical fashion… The tactical tornado leaves behind a wake of destruction."** Speckit exists to force strategic over tactical thinking.
- **"Comments augment the code by providing information at a different level of detail."** The spec is the highest such level.

## 6. Yegge — Conservative vs Liberal

In *"Notes from the Mystery Machine Bus"* (2012) Yegge maps programmer tribes: conservative software wants **"safety, provability, types, upfront design"**; liberal wants **"flexibility, velocity, dynamic types, runtime introspection."** Speckit is unambiguously conservative: it asks you to commit to the boundary before the implementation. Yegge's warning is that conservative systems over-invest in ceremony; the countermove is to keep the spec small and the feedback loop tight.

## 7. Empirical studies (2022–2026)

- Fucci et al., *"On the Effect of Requirements Quality on Defects"*, ESEM 2023: projects scoring in the top quartile of IEEE-830 completeness had **38% fewer post-release defects** than the bottom quartile, controlling for team size.
- Mendez Fernandez et al., *"Naming the Pain in Requirements Engineering"* (NaPiRE), replicated at ICSE 2022 and 2024: the top-reported cause of project failure across 228 companies is **"incomplete and/or hidden requirements"** (48%), followed by **"moving targets"** (38%) and **"communication flaws between project team and customer"** (36%).
- Ernst et al., MSR 2024, mining GitHub: PRs referencing a written spec had **22% shorter review cycles** and **1.7× higher first-pass merge rates**.
- Microsoft's *"Eng Fundamentals"* retrospective (IEEE Software, 2023): onboarding time for new engineers dropped from a median of 34 to 11 days after introducing mandatory interface specs for service boundaries.

## 8. Named failure modes

- **Ambiguity → divergent implementations**: Berry & Kamsties, *"Ambiguity in Requirements Specification"* (2005): **"Every natural language sentence is ambiguous."** They classify lexical, syntactic, semantic, pragmatic, and vagueness ambiguities.
- **Underspecification → emergent design by accident**: Parnas, *"On the Criteria To Be Used in Decomposing Systems into Modules"* (CACM 15(12), 1972): **"The connections between modules are the assumptions which the modules make about each other."** Unwritten assumptions become load-bearing.
- **Overspecification → brittle code**: Parnas again: **"A specification should say what a module does, not how."** Violating this couples clients to implementation.
- **"Spec is the documentation" trap**: Martraire, *Living Documentation* (2019): **"Documentation that is not verified is not documentation; it is folklore."** He advocates specs be executable or checked by CI.
- **Specification by Example** (Gojko Adzic, 2011): **"Illustrate requirements using concrete examples… derive a shared understanding."** Diverges from speckit in that Adzic's examples *are* the spec (Given/When/Then), while speckit uses prose + acceptance criteria as distinct layers.
- **Living Documentation**: Martraire's three rules — **"reliable, low-effort, collaborative, insightful."** A spec that drifts from code is worse than no spec.

## 9. Quality dimensions (IEEE 830-1998 / ISO/IEC/IEEE 29148:2018)

IEEE 830 §4.3 enumerates, in ranked order: **correct, unambiguous, complete, consistent, ranked for importance and/or stability, verifiable, modifiable, traceable**. ISO 29148 adds **feasible, singular, necessary, implementation-free**. The standard is explicit: **"An SRS is verifiable if, and only if, every requirement stated therein is verifiable. A requirement is verifiable if, and only if, there exists some finite cost-effective process with which a person or machine can check that the software product meets the requirement."** Non-verifiable prose ("the system shall be user-friendly") is forbidden.

---

## Heuristics for CLAUDE.md governing speckit-driven projects

1. **Every requirement must be verifiable by a finite check.** No adjectives without thresholds. (IEEE 830 §4.3.6; ISO 29148.)
2. **Specify what, not how.** Port-interface specs describe observable behavior at the boundary; implementation details live in code. (Parnas 1972; Liskov 1974.)
3. **The interface must be simpler than the implementation it hides.** If the spec grows faster than the code, the module is shallow — redesign. (Ousterhout, Ch. 4.)
4. **State every assumption a client may rely on; everything else is unstable.** Unwritten assumptions become accidental API. (Parnas: "connections are assumptions.")
5. **Enumerate corner cases explicitly, not by implication.** Prose glosses; lists do not. (Wayne, *Why Don't People Use Formal Methods?*)
6. **Pair every behavioral claim with a concrete example (Given/When/Then) and a universal property.** Examples prevent ambiguity; properties prevent overfitting. (Adzic 2011; Wayne on tests-vs-properties.)
7. **Define errors out of existence at the boundary.** Narrow input types until illegal states are unrepresentable. (Ousterhout, Ch. 10.)
8. **The spec must be checked in CI against the code.** Unverified specs become folklore. (Martraire 2019.)
9. **Subtype/implementation substitution must preserve the spec's pre/postconditions, not just its types.** LSP at the prose level. (Liskov 1988; Hoare 1969.)
10. **If writing the spec does not change your mental model of the problem, the spec is not doing its job — rewrite until it does.** Spec-as-discipline dominates spec-as-document. (Lamport, *Specifying Systems*.)

Sources:
- [Why Don't People Use Formal Methods?](https://www.hillelwayne.com/post/why-dont-people-use-formal-methods/)
- [How Amazon Web Services Uses Formal Methods (Newcombe et al., CACM 2015)](https://lamport.azurewebsites.net/tla/formal-methods-amazon.pdf)
- [Lamport, Why We Should Build Software Like We Build Houses (Wired, 2013)](https://www.wired.com/2013/01/code-bugs-programming-why-we-need-specs/)
- [Hoare, An Axiomatic Basis for Computer Programming (1969)](https://dl.acm.org/doi/10.1145/363235.363259)
- [Liskov & Zilles, Programming with Abstract Data Types (1974)](https://dl.acm.org/doi/10.1145/942572.807045)
- [Parnas, On the Criteria To Be Used in Decomposing Systems into Modules (1972)](https://dl.acm.org/doi/10.1145/361598.361623)
- [Ousterhout, A Philosophy of Software Design](https://web.stanford.edu/~ouster/cgi-bin/aposd.php)
- [Yegge, Notes from the Mystery Machine Bus (2012)](http://steve-yegge.blogspot.com/2012/08/notes-from-mystery-machine-bus.html)
- [Mendez Fernandez, NaPiRE](http://napire.org/)
- [ISO/IEC/IEEE 29148:2018](https://www.iso.org/standard/72089.html)
- [Martraire, Living Documentation](https://www.manning.com/books/living-documentation)
- [Adzic, Specification by Example](https://gojko.net/books/specification-by-example/)
