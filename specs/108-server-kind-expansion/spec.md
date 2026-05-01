# Feature 108 — JID server-kind expansion (hosted, bot, channel, broadcast)

**Branch**: `108-server-kind-expansion`
**Status**: implemented
**Source**: spec 105 follow-up. Spec 105 promoted LID to first-class.
This feature expands the domain JID parser to recognise the rest of
WhatsApp's namespace surface (per `whatsmeow/types/jid.go` line 22-33),
either accepting them as new addressable kinds or refusing them with a
typed sentinel — never letting an unknown server fall through silently.

## Problem

After spec 105 the parser accepts only `@s.whatsapp.net`, `@lid`, and
`@g.us`. WhatsApp's protocol exposes more namespaces and any of them
can arrive in an inbound event, the allowlist file, or a `wa send`
target. Pre-spec-108 they all fail with the generic "unknown server"
error, which conflates four very different cases:

| Server          | Reality                                       | Pre-108 behaviour |
|-----------------|-----------------------------------------------|-------------------|
| `hosted`        | enterprise PN tenant                          | ErrInvalidJID     |
| `hosted.lid`    | enterprise LID tenant                         | ErrInvalidJID     |
| `bot`           | Meta AI and other first-party bots            | ErrInvalidJID     |
| `newsletter`    | WhatsApp Channels                             | ErrInvalidJID     |
| `broadcast`     | broadcast lists — banned by safety policy     | ErrInvalidJID     |

The generic refusal hides the safety policy difference (`broadcast`
must be hard-rejected per CLAUDE.md), conflates "we should accept
this" with "we shouldn't" for hosted / bot / channel, and leaves the
allowlist loader unable to tell the operator why a row was rejected.

## Decision

Expand the parser switch:

- **`hosted`** + **`hosted.lid`**: ACCEPT as addressable. New
  `IsHosted()` discriminator covers both variants. Numeric user part,
  same shape as PN/LID.
- **`bot`**: ACCEPT as addressable. New `IsBot()` discriminator.
  Numeric user part.
- **`newsletter`**: ACCEPT as a non-addressable kind. New
  `IsChannel()` discriminator. NOT in `IsAddressable()` because
  channels are read-only fan-out (no per-recipient address). User
  part follows the group/numeric+hyphen shape (matches whatsmeow's
  validator).
- **`broadcast`**: REJECT with the new typed sentinel
  `domain.ErrBroadcastForbidden` per CLAUDE.md §Safety: "no broadcast
  lists ever". Wire-mapped to `-32100 PolicyRefused` so the wire
  shape matches every other safety refusal.
- **`msgr`** + **`interop`**: NOT recognised. Continue to fail with
  the existing "unknown server" `ErrInvalidJID`. Both are
  cross-network interop bridges (Messenger, WhatsApp/Messenger
  consolidation); the daemon does not yet have a use case for either,
  and accepting them speculatively would commit to surface area we
  cannot test. Add when first observed in production traffic.
- **`c.us`** (legacy WhatsApp PN, pre-Multi-Device): NOT recognised.
  WhatsApp migrated everyone off `c.us` years ago; whatsmeow keeps it
  in the constant table for archival reasons. Accepting would be an
  alias for `s.whatsapp.net` with no behavioural difference but
  would expand the parser's surface for zero gain. Add when an
  operator actually hits this in 2026+.

`IsAddressable()` widens to `{user, LID, hosted, hosted.lid, bot}`.
The 5 callsites already on `IsAddressable()` (post-spec 105) get the
new kinds for free.

## Alternatives rejected

Per Constitution rule 20.

### A. Accept every whatsmeow-known server uniformly

We could add every constant from `whatsmeow/types/jid.go` to the
parser. **Rejected** because:

1. `broadcast` must be HARD-REFUSED per CLAUDE.md §Safety.
2. `msgr` / `interop` / `c.us` have no concrete use case today and
   the daemon has no test fixtures for them. Constitution rule 1
   ("smallest change that satisfies the request") forbids speculative
   surface.
3. Different namespaces have different addressability semantics
   (channels are not addressable for direct send, broadcasts are
   refused outright). A uniform "accept" would erase those distinctions.

### B. Quietly map `broadcast` to `ErrInvalidJID`

The pre-108 behaviour. **Rejected** because the operator gets no
actionable feedback when `wa allow add 12345@broadcast` fails — they
might assume the JID is malformed and waste time fixing it. The typed
`ErrBroadcastForbidden` says "this isn't going to work no matter how
you spell it; safety policy refuses".

### C. Accept `c.us` as an alias for `s.whatsapp.net`

Treat `c.us` as a synonym, normalising the user part with the
`s.whatsapp.net` server during Parse. **Rejected** because:

1. The round-trip invariant (`Parse(j.String()) == j`) would break
   unless we preserve the original suffix.
2. Aliasing creates a hidden equivalence that the allowlist would
   then need to handle (does granting `5511...@s.whatsapp.net`
   authorise `5511...@c.us`?). Cross-namespace privacy boundaries
   are load-bearing per spec 105 FR-009.
3. `c.us` is functionally extinct in 2026.

## Functional requirements

- **FR-001** — `domain.Parse("<digits>@hosted")` succeeds with
  `IsHosted() = true`, `IsUser() = false`, `IsLID() = false`,
  `IsAddressable() = true`.
- **FR-002** — `domain.Parse("<digits>@hosted.lid")` succeeds with
  `IsHosted() = true`, `IsLID() = false`, `IsAddressable() = true`.
- **FR-003** — `domain.Parse("<digits>@bot")` succeeds with
  `IsBot() = true`, `IsAddressable() = true`.
- **FR-004** — `domain.Parse("<group-shape>@newsletter")` succeeds
  with `IsChannel() = true`, `IsAddressable() = false`,
  `IsGroup() = false`.
- **FR-005** — `domain.Parse("<digits>@broadcast")` fails with
  `domain.ErrBroadcastForbidden`. The socket dispatcher maps this
  sentinel to `-32100 PolicyRefused`.
- **FR-006** — Round-trip invariant holds for every new kind:
  `Parse(j.String()) == j`.
- **FR-007** — Cross-namespace inequality: a PN, a LID, a hosted, a
  hosted.lid, and a bot with the same user digits are five distinct
  values (Go struct equality on `(user, server)`).
- **FR-008** — `IsAddressable()` widens to
  `{user, LID, hosted, hosted.lid, bot}`. Channels and groups remain
  excluded.
- **FR-009** — `msgr`, `interop`, and `c.us` continue to fail with
  the generic `ErrInvalidJID` "unknown server" error — explicitly
  out of scope.

## Tests

- `internal/domain/jid_test.go` — parse table extended with 8 cases
  covering each new accepted kind plus the broadcast refusal.
  `TestJID_RoundTrip` extended with 4 new round-trips.
  `TestJID_Discriminators` pins `IsHosted`, `IsBot`, `IsChannel`,
  and the `IsAddressable` widening.
- `internal/domain/jid_fuzz_test.go` — seed corpus extended with the
  five new namespaces; invariant updated to validate against the
  expanded server set. Fuzz survives 237k execs with no
  counterexample.
- `internal/adapters/secondary/whatsmeow/translate_jid_test.go` —
  `TestToDomain_BroadcastRefused` pins the typed sentinel surfaces at
  the adapter boundary; `TestToDomain_InvalidString` retargeted to
  use `interop` (still in the deferred set).

## Out of scope

- `c.us` legacy PN alias, `msgr` Messenger interop, `interop` cross-
  network bridge — accept when first observed in production.
- Bot allowlist policy carve-outs (e.g. refuse `group.add` for `@bot`
  identities). Add when the allowlist UX surfaces bot interactions
  distinctly.
- Channel admin / publish surface (`channel.publish`,
  `channel.followers.list`). Newsletters today are read-only via
  `groups.get`-style metadata reads; outbound publish is a separate
  feature.
- Domain types for the `bot` profile metadata (`MetaAIJID =
  "13135550002@s.whatsapp.net"` is also recognised as the legacy form;
  `NewMetaAIJID = "867051314767696@bot"` is the modern form per
  whatsmeow's `types/jid.go`).

## References

- whatsmeow source — `types/jid.go:22-33`: full known-server table.
- whatsmeow source — `types/jid.go:46-48`: `MetaAIJID` /
  `NewMetaAIJID` constants demonstrating the bot namespace's role.
- CLAUDE.md §Safety — "no broadcast lists ever".
- Spec 105 — domain JID parser accepts LID; spec 108 expands.
- Spec 106 — `IdentityResolver` port for PN ↔ LID translation.
