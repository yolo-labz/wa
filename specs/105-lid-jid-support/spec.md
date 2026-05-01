# Feature 105 — LID JID support (`@lid` server)

**Branch**: `105-lid-jid-support`  
**Status**: implemented  
**Source**: ad-hoc bug report from operator on 2026-04-30, surfaced after
WhatsApp transition to LID-default identity for non-PN contact-discovery
flows.

## Problem

`wa send --to 66448177246461@lid` fails with `rpc error -32015 invalid
JID` even though the contact is reachable in the WhatsApp app and the
underlying `whatsmeow` client supports LID addressing natively.

Root cause: `internal/domain/jid.go` rejects any server other than
`s.whatsapp.net` and `g.us` at parse time. WhatsApp now returns LIDs in
place of phone-number JIDs whenever the contact's PN was never disclosed
to this account — LinkedIn-click-to-WA deep links, business-discovery
flows, group joins via invite, and as the new default identity for fresh
Multi-Device sessions. Operator ran into this with TELUS Ricardo (a
LinkedIn-routed contact); the daemon refuses every send to `@lid`.

## Decision

Promote LID to a **first-class addressable JID kind** in the domain
layer, peer to the existing phone-number user JID. The domain stays
infrastructure-free: a LID is just `<digits>@lid`, parsed by the same
canonical-string round-trip as PN and group JIDs, with a separate
namespace from PN (a LID and a PN with identical user digits are
distinct values).

Concretely:

- `internal/domain/jid.go` adds `serverLID = "lid"` (matches whatsmeow's
  `types.HiddenUserServer`), accepts `<digits>@lid` in `parseJIDForm`,
  and exposes `IsLID()` plus `IsAddressable()` (= `IsUser() || IsLID()`)
  helpers.
- `internal/adapters/secondary/whatsmeow/translate_jid.go` requires no
  changes: it already uses canonical-string round-trip via
  `waTypes.ParseJID`, which has long supported the `@lid` suffix.
- The allowlist parser at `cmd/wad/allowlist.go` requires no changes:
  it delegates to `domain.Parse`.
- The fuzz invariant in `internal/domain/jid_fuzz_test.go` is widened
  from "exactly one of {user, group}" to "exactly one of {user, LID,
  group}".

## Alternatives rejected

Per Constitution rule 20 (Nygard ADR / MADR completeness;
`.specify/memory/constitution.md` line 138), every architectural decision
MUST list at least one rejected alternative with its reason.

### B. Pre-resolve LID → PN at allowlist-add time

A new `wa contact resolve --lid <jid>` subcommand would call
`whatsmeow.Client.Store.LIDs.GetPNForLID(ctx, lid)` and cache the PN,
keeping the domain JID validator PN-only. **Rejected** because:

1. Many LIDs have no PN at all from this account's perspective — the
   exact privacy property WhatsApp engineered LIDs to provide. Forcing
   PN resolution turns "send to LinkedIn contact" into "fail unless they
   reply first" (whatsmeow issue #871 is the canonical write-up).
2. WhatsApp is migrating outbound encryption identity to LID-first
   (`whatsmeow/send.go` calls `LIDs.GetLIDForPN` on the PN path before
   encrypting). The native primitive is LID; PN is the alias. Inverting
   that in the domain forces every send through a translation layer
   that whatsmeow itself is moving away from.
3. Mautrix-whatsapp settled on the same answer in its v0.5.0 LID
   migration (release notes, January 2026): "LID identifiers are
   bridged as `lid-<number>` instead of just `<phone number>`."

### C. Accept LID via a `--lid-jid` CLI flag bypassing the domain validator

A one-off escape hatch in `cmd/wa/cmd_send.go` that constructs a
`waTypes.JID{Server: "lid"}` directly, bypassing
`domain.Parse`. **Rejected** because:

1. Constitution rule 23 forbids infrastructure types in port
   signatures. The send-message use case takes a `domain.JID`; a CLI
   flag that produces a `waTypes.JID` would have to leak past the
   domain boundary.
2. Allowlist policy is keyed on `domain.JID`. Bypassing the validator
   means LID contacts cannot appear in the allowlist, defeating the
   single safety primitive the daemon is built around.
3. `--force` flags are forbidden by repository convention (no `--force`
   anywhere in the CLI surface; see `cmd/wa` for the audit trail). The
   escape hatch is the wrong shape on principle.

## Functional requirements

- **FR-001** — `domain.Parse("<digits>@lid")` succeeds and returns a JID
  with `IsLID() = true`, `IsUser() = false`, `IsGroup() = false`.
- **FR-002** — `domain.Parse("<non-digits>@lid")` and
  `domain.Parse("@lid")` fail with `ErrInvalidJID`. The user part of a
  LID MUST be a non-empty digit string. (No length range — WhatsApp
  validates at the protocol layer; observed LIDs vary 12–17 digits.)
- **FR-003** — Round-trip invariant holds: for every successfully parsed
  `j`, `Parse(j.String()) == j`.
- **FR-004** — `j.IsAddressable()` is true iff `j.IsUser() || j.IsLID()`.
  Group JIDs are NOT addressable for direct send (they go through
  `GroupManager`).
- **FR-005** — A LID and a PN with identical user digits are distinct
  values (`!= equality`). They live in separate namespaces.
- **FR-006** — `whatsmeow.toWhatsmeow(domain.MustJID("<digits>@lid"))`
  yields a `waTypes.JID` whose `Server == waTypes.HiddenUserServer`.
- **FR-007** — `allowlist.toml` accepts `<digits>@lid` rows
  transparently — no parser changes needed because the loader delegates
  to `domain.Parse`.
- **FR-008** — `domain.NewGroup` and `Group.WithAdmins` accept LID
  participants and admins (`IsAddressable()` gate). `whatsmeow`
  group-info translation and the GroupAdminAdapter `Create` /
  `AddParticipants` / `RemoveParticipants` / `Promote` / `Demote`
  validators do likewise. A LID-only roster MUST round-trip through
  inbound translation without truncation.
- **FR-009** — Cross-namespace privacy boundary: granting `send` to
  `<digits>@s.whatsapp.net` MUST NOT authorise `<digits>@lid` (and
  vice versa). `Allowlist.Allows(jid, action)` keys on the full
  `(user, server)` tuple via Go map equality.

## Out of scope

The names and signatures in this section are illustrative — concrete
field/type authority is deferred to a future feature's `data-model.md`
per Constitution rule 5. Nothing in this PR depends on them.

- **PN ↔ LID resolution port.** A future feature should add an app-level
  resolver port (something like `ResolveLID(ctx, lid) (pn JID, error)` /
  `ResolvePN`) backed by `whatsmeow.Client.Store.LIDs`. Useful for
  surface UX ("send to Ricardo at +1-604-...") but not required to
  unblock the operator's current send.
- **Addressing-mode preservation in inbound events.** whatsmeow exposes
  `types.MessageInfo.Sender` as either PN or LID depending on which
  namespace the chat was last seen in; if both are known, the message
  metadata carries the addressing mode. Surfacing both forms in the
  history store schema is a separate v0.6 feature.
- **`wa contact lid <pn>` / `wa contact pn <lid>` resolver subcommands.**
  Wait until the resolver port lands.
- **Hosted-LID server (`hosted.lid`) and hosted PN server (`hosted`).**
  Used by enterprise customers. Add when first observed in the wild.
- **Bot, broadcast, newsletter, msgr, interop, c.us legacy servers.**
  Add per use case when needed; LID is the only one currently blocking
  an operator.

## Tests

- `internal/domain/jid_test.go` — parse table extended with
  `canonical_lid`, `long_lid`, `lid_non_digit_user`, `lid_empty_user`;
  round-trip case for `66448177246461@lid`; `TestJID_Discriminators`
  pins `IsLID`, `IsAddressable`, and the namespace-distinctness
  property between PN and LID.
- `internal/domain/jid_fuzz_test.go` — seed corpus extended with LID
  inputs; invariant strengthened to "exactly one of {user, LID,
  group}".
- `internal/adapters/secondary/whatsmeow/translate_jid_test.go` —
  `TestToDomain_RoundTrip` extended with a LID; new `TestToWhatsmeow_LID`
  pins the `Server == HiddenUserServer` post-condition.

## References

- whatsmeow source — `types/jid.go` lines 22–33: full known-server table
  (`s.whatsapp.net`, `g.us`, `c.us`, `broadcast`, `lid`, `msgr`,
  `interop`, `newsletter`, `hosted`, `hosted.lid`, `bot`).
- whatsmeow source — `store/store.go` lines 179–183: `LIDStore`
  interface (`PutLIDMapping`, `GetPNForLID`, `GetLIDForPN`).
- whatsmeow source — `send.go` lines 328 and 1076: outbound send path
  calls `Store.LIDs.GetLIDForPN(ctx, to)` so the encryption identity is
  LID-based even when the recipient JID is a PN.
- whatsmeow issue #871 (closed): "Should we retry resolving LID to PN
  after recent changes?" — confirms `GetPNForLID` may legitimately
  return an empty PN.
- mautrix-whatsapp v0.5.0 migration notes — January 2026 LID-handling
  fixes: avatar duplication across PN and LID ghosts, group-LID
  migration first-message ghost, read-receipt routing in LID DMs.
- WAHA contacts doc and Baileys WhatsApp-IDs concepts page — both
  document `lid` as the WhatsApp-known JID server for hidden user
  identifiers.
