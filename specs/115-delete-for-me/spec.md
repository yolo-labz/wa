# Feature 115 — `message.revoke scope=self` actually deletes for me

**Branch**: `115-delete-for-me` (compact spec: design + code in one PR)
**Status**: drafted + implemented in the same PR
**Source**: a downstream consumer (`n8n-flows#18`, family-group filter) needed
"delete this message from my own WhatsApp" and discovered `scope=self` does not
do it.

## Problem

`message.revoke {scope:"self"}` claims to be the delete-for-me half of
FR-014, and it is not. `Moderator.Revoke`
(`internal/adapters/secondary/whatsmeow/moderator.go:99-107`, pre-change) sends
the ProtocolMessage REVOKE tombstone only for `RevokeEveryone`; for
`RevokeSelf` it falls straight through to `m.history.StampRevoke` and returns.
The port contract states the behaviour as a requirement, not an omission —
*"Revoke with RevokeSelf touches local persistence only and MUST NOT emit a
network tombstone"* (`internal/app/ports_018.go:31-33`, pre-change) — and
`TestRevokeSelfLocalOnly` pins it.

So the only observable effect of `scope=self` is that `wa history` stops
returning the row. The message is still on the caller's phone, still on their
other linked devices, and still in every future history sync. A caller who asks
the daemon to delete a message for themselves is told `{}` — success — and
nothing is deleted.

That is the defect: **the method reports success for work it never attempted.**
Not a missing feature request, a false success (CLAUDE.md rule 12 — no silent
fallbacks).

The confusion is understandable. WhatsApp has two unrelated mechanisms and the
daemon only ever wired one:

| | delete for everyone | delete for me |
|---|---|---|
| transport | `ProtocolMessage{type:REVOKE}`, an ordinary encrypted message | an **app-state** mutation in the `regular_high` collection |
| API | `Client.BuildRevoke` + `SendMessage` | `Client.SendAppState(appstate.PatchInfo)` |
| who sees it | every participant | only the sender's own devices |
| revocable | no | no |

whatsmeow ships a convenience builder for exactly one of the two. `appstate`
exports `BuildArchive`, `BuildDeleteChat`, `BuildLabelChat`, `BuildLabelEdit`,
`BuildLabelMessage`, `BuildMarkChatAsRead`, `BuildMute`, `BuildMuteAbs`,
`BuildPin`, `BuildSettingPushName`, `BuildStar` — and no
`BuildDeleteMessageForMe`. The absence of the *builder* was mistaken for the
absence of the *capability*.

## Decision

Build the patch here. Every part whatsmeow does not assemble, it does export:

- `appstate.PatchInfo` and `appstate.MutationInfo` are public structs with
  public fields (`appstate/encode.go:19-37`), so an external caller can
  construct an arbitrary mutation.
- `appstate.IndexDeleteMessageForMe = "deleteMessageForMe"` already exists
  (`appstate/keys.go:117`), in the **`regular_high`** index block.
- `waSyncAction.SyncActionValue` carries
  `DeleteMessageForMeAction{DeleteMedia *bool, MessageTimestamp *int64}` as
  field 18.
- `SendAppState(ctx, appstate.PatchInfo) error` is already on this repo's
  `whatsmeowClient` interface
  (`internal/adapters/secondary/whatsmeow/whatsmeow_client.go:111`), wired for
  the labels adapter.

### Index shape

The mutation index is a `[]string` that both endpoints must compute
identically. `BuildStar` is the in-library precedent — same `regular_high`
collection, same per-message addressing (`appstate/encode.go:233-243`):

```go
Index:   []string{IndexStar, targetJID, messageID, fromMe, senderJID},
Version: 2,
```

where `fromMe` is `"1"`/`"0"` and `senderJID` collapses to `"0"` when the
message is not from a third party (`appstate/encode.go:251-254`).

`deleteMessageForMe` takes the same five parts with **`Version: 3`**. That
version is not inferred from the star mutation: it is confirmed against an
independent implementation, Baileys `src/Utils/chat-utils.ts:633-646`:

```ts
index: ['deleteMessageForMe', jid, key.id!, key.fromMe ? '1' : '0', '0'],
type: 'regular_high',
apiVersion: 3,
```

**One documented divergence.** Baileys hardcodes the fifth part to `'0'`;
whatsmeow's `BuildStar` passes the real participant JID and collapses to `"0"`
only for a non-third-party sender. This implementation follows **whatsmeow**,
because the patch is encoded by whatsmeow's own `EncodePatch` and the star
mutation is the closest same-collection precedent — and because Baileys'
`chatModify` has no participant in scope at that call site, which reads as an
incomplete group path rather than a deliberate protocol claim. FR-115-6 pins
the group case so a live disagreement is caught as a failing test, not as
silent non-deletion.

### Ordering

`scope=everyone` sends the tombstone *before* stamping locally, because a
failed network call must not leave a row the caller believes dead while peers
still see it (`moderator.go:96-99`). `scope=self` inherits the same rule for
the same reason: `SendAppState` first, `StampRevoke` only on success.

## Requirements

- **FR-115-1** `Revoke(scope=self)` MUST push an app-state patch with
  `Type = WAPatchRegularHigh`, exactly one mutation, `Version = 3`, and
  `Value.DeleteMessageForMeAction` non-nil.
- **FR-115-2** The mutation index MUST be
  `["deleteMessageForMe", <chat JID>, <message id>, "1"|"0", <participant>]`,
  where element 3 is `"1"` iff the target message was sent by this account,
  and element 4 is the sender JID for a third-party message or `"0"` otherwise.
- **FR-115-3** `DeleteMessageForMeAction.MessageTimestamp` MUST be the original
  message's timestamp in **milliseconds**, read from the history store — not
  the current clock.
- **FR-115-4** `Revoke(scope=self)` MUST NOT call `BuildRevoke` or
  `SendMessage`. Delete-for-me is never visible to a peer.
- **FR-115-5** On `SendAppState` failure the local row MUST NOT be stamped, and
  the error MUST propagate.
- **FR-115-6** For a group message from a third party, index element 4 MUST be
  that participant's JID; for a 1:1 message it MUST be `"0"`.
- **FR-115-7** When the target message is absent from the history store the
  call MUST fail with `domain.ErrMessageUnknown` → `-32302`, never a
  best-effort patch. The index cannot be computed without the row, and a wrong
  index deletes nothing while reporting success — the exact defect this feature
  removes.
- **FR-115-8** `Revoke(scope=self)` MUST refuse when disconnected
  (`domain.ErrDisconnected`), matching `scope=everyone`.
- **FR-115-9** `scope=everyone` behaviour is unchanged: tombstone, then stamp,
  and no app-state patch.

## Contract change

`internal/app/ports_018.go` MUST stop requiring local-only behaviour for
`RevokeSelf`. The new wording states the mechanism, so the next reader does not
re-derive the two-mechanism table above.

`TestRevokeSelfLocalOnly` asserted the defect. It is replaced by
`TestRevokeSelfSendsAppStatePatch`, which keeps the half that is still true
(no `BuildRevoke`, no `SendMessage`) and adds the half that was missing.

## What this feature does NOT do

- It does not delete media files off disk. `DeleteMessageForMeAction.DeleteMedia`
  is sent as `false`; media GC is `media.gc`'s job and coupling them would make
  one irreversible action perform two.
- It does not add a bulk or by-sender variant. One message per call; a caller
  that wants 300 makes 300 calls and owns its own rate budget.
- It does not make `scope=self` reversible. Nothing in WhatsApp's protocol
  undoes a delete-for-me, and the daemon must not imply otherwise.

## Verification

FR-115-1..9 are unit-verifiable against the fake client and are covered by
`TestRevokeSelfSendsAppStatePatch`, `TestRevokeSelfGroupParticipantIndex`,
`TestRevokeSelfUnknownMessage`, `TestRevokeSelfAppStateFailureDoesNotStamp`,
`TestRevokeSelfDisconnected`, and the unchanged
`TestRevokeEveryoneProtocolMessage`.

**The wire cannot be unit-tested.** Whether WhatsApp's servers and the caller's
phone accept this exact index is only answerable against a live paired session.
`docs/runbooks/delete-for-me-live-probe.md` is the falsifiable procedure:
send a throwaway message to your own self-chat, revoke it with `scope=self`,
and confirm it disappears from the phone. Until that probe has been run on a
given deployment, this feature is implemented-but-unproven (CLAUDE.md rule 29 —
mark status honestly).
