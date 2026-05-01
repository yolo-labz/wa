# Feature 106 — IdentityResolver port (PN ↔ LID translation)

**Branch**: `106-identity-resolver-port`
**Status**: implemented
**Source**: spec 105 follow-up. Spec 105 made LIDs first-class
addressable identities at the parser; this feature surfaces the
*mapping* between PN and LID at the use-case layer so callers can ask
"do we know the phone number behind this LID yet?" without crossing
the hexagonal boundary.

## Problem

After spec 105, `wa send --to <digits>@lid` works — but the operator
has no way to discover the phone number behind a LID, nor to pin a
known PN ↔ LID pair into the daemon's mapping store. Both directions
are essential:

- **LID → PN**: useful for surface UX ("send to Ricardo at
  +1-604-..." instead of opaque digits).
- **PN → LID**: required by whatsmeow's send pipeline, which already
  calls `Store.LIDs.GetLIDForPN` internally before encrypting (see
  `whatsmeow/send.go:328`); exposing this lets callers verify the
  mapping is established before relying on it.

Both directions must surface "no mapping yet known" without erroring,
because most LIDs legitimately have no PN counterpart from this
account's perspective until the contact replies — that is the privacy
property WhatsApp engineered LIDs to provide (whatsmeow issue #871).

## Decision

Add a new app port `IdentityResolver` with three methods:

```go
type IdentityResolver interface {
    ResolveLID(ctx, pn domain.JID) (domain.JID, error)
    ResolvePN(ctx, lid domain.JID) (domain.JID, error)
    RecordMapping(ctx, pn, lid domain.JID) error
}
```

A successful call MAY return the zero `domain.JID` with `nil` err to
signal "no mapping yet". Callers MUST NOT treat zero as an error.

JSON-RPC surface adds one method `contact.resolve` (singular `contact`
to fit the existing `contact.block`/`contact.unblock` namespace, not
the `contacts.*` directory namespace):

```jsonc
// Params
{"jid": "<pn or lid>"}

// Result
{
  "input": "<canonical input JID>",
  "alt":   "<resolved alternate-namespace JID, or empty>",
  "kind":  "pn" | "lid",
  "known": true | false
}
```

CLI surface adds two subcommands under the existing `wa contact`
parent:

- `wa contact lid <pn>` — print the LID for a phone-number JID
- `wa contact pn <lid>` — print the PN for a LID

Both print the resolved JID followed by a newline on stdout, or
nothing when no mapping is known yet (exit 0 in both cases — callers
branch on stdout being empty). With `--json` they print the full
envelope.

## Alternatives rejected

Per Constitution rule 20.

### A. Two methods (`contact.lid` and `contact.pn`) instead of one symmetric `contact.resolve`

Two methods on the wire = two handlers in the dispatcher = two
allowlist rows + two rate-limit buckets. **Rejected** because the two
operations are byte-identical at the dispatcher boundary; the
semantic difference lives entirely inside the resolver port and the
wire result already carries `kind` for client-side branching.
Symmetric `contact.resolve` keeps the surface area minimal.

### B. Inline LID resolution into `contacts.lookup`

The existing `contacts.lookup` returns a `Contact` view by JID. Adding
LID/PN siblings to that envelope would conflate "directory metadata"
(push name, verified flag, business profile) with "namespace mapping"
(LID ↔ PN). **Rejected** because the two concerns have different
freshness semantics: directory data is cached for 60s with TTL, while
LID mappings are persistent and update only on rare events. Bundling
them forces the cache TTL on the mapping or breaks the `Contact`
envelope's freshness contract.

### C. Eagerly resolve every LID at allowlist-add time and store both forms

`wa allow add 66448177246461@lid` could call `ResolvePN` and pin both
JIDs in the allowlist atomically. **Rejected** because:

1. Most LIDs have no PN counterpart yet — the allowlist add would
   block on a network call that legitimately returns "unknown".
2. Constitution rule 1 forbids the allowlist from holding speculative
   data; every grant is an explicit operator decision.
3. The privacy boundary between PN and LID namespaces (spec 105
   FR-009) is load-bearing; eagerly conflating them defeats it.

## Functional requirements

- **FR-001** — `IdentityResolver.ResolveLID(ctx, pn)` returns the LID
  associated with `pn`, or the zero `domain.JID` (with `nil` err) if
  no mapping is known. `pn` MUST satisfy `IsUser()`; otherwise the
  method returns `app.ErrNotIdentity`.
- **FR-002** — `IdentityResolver.ResolvePN(ctx, lid)` returns the PN
  associated with `lid`, or the zero `domain.JID` (with `nil` err) if
  no mapping is known. `lid` MUST satisfy `IsLID()`; otherwise
  `app.ErrNotIdentity`.
- **FR-003** — `IdentityResolver.RecordMapping(ctx, pn, lid)` stores
  the pair in both directions. Argument order is fixed (`pn` first,
  `lid` second); swapped args return `app.ErrNotIdentity`. Repeated
  calls with the same pair are idempotent.
- **FR-004** — `contact.resolve` JSON-RPC method auto-detects the
  input kind via `IsUser()` / `IsLID()` and dispatches to the right
  resolver method. Group/zero/other JID kinds return
  `-32015 InvalidJID` (via `ErrNotIdentity`).
- **FR-005** — `wa contact lid <pn>` prints the LID counterpart on
  stdout (or nothing on miss). `wa contact pn <lid>` prints the PN
  counterpart on stdout (or nothing on miss). Both exit 0 on miss.
- **FR-006** — When the input kind disagrees with the subcommand
  (e.g. `wa contact lid 66448177246461@lid`), the CLI exits 64 with
  a clear error message — the user passed the wrong shape.
- **FR-007** — `Identity` is an OPTIONAL field on `DispatcherConfig`.
  When nil, `contact.resolve` returns `-32601 MethodNotFound`,
  matching the existing optional-port pattern (Moderator,
  ChatStateManager, Blocker, etc.).

## Out of scope

- **Bulk resolution** (`GetManyLIDsForPNs`). whatsmeow's `LIDStore`
  exposes a batch API; we expose only the single-shot variants.
  Add when a use case needs it.
- **Auto-RecordMapping on inbound events**. whatsmeow surfaces PN ↔ LID
  pairs as part of `events.Message` and other events. A future
  feature could wire those event payloads into `RecordMapping` so the
  daemon learns mappings without operator action. Today the operator
  must call `wa contact lid` / `pn` explicitly.
- **Allowlist auto-promotion**. When `RecordMapping` learns a new
  mapping for an allowlisted JID, the corresponding LID/PN could be
  auto-allowlisted with the same actions. This is a policy decision
  with privacy implications and warrants its own spec.

## Tests

- `internal/adapters/secondary/memory/identityresolver_test.go` —
  pins FR-001/002/003/004 against the deterministic in-memory map.
- `internal/adapters/secondary/whatsmeow/identity_resolver_test.go` —
  pins FR-001/002/003 against the fakeWhatsmeowClient stub, including
  the "miss" path (whatsmeow returns `waTypes.EmptyJID`).
- `internal/app/method_identity_test.go` — pins FR-004/005/007 at the
  dispatcher boundary using a stub IdentityResolver.

## References

- whatsmeow source — `store/store.go:179-183`: `LIDStore` interface.
- whatsmeow source — `send.go:328,1076`: outbound encryption identity
  is LID-first via `Client.Store.LIDs.GetLIDForPN(ctx, to)`.
- whatsmeow issue #871 — confirms `GetPNForLID` legitimately returns
  the empty JID with nil err on miss.
- Spec 105 — domain JID parser accepts LID; this feature builds on
  that primitive.
