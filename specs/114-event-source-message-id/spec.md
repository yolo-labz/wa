# Feature 114 — structural `messageId` on message events

**Branch**: `361-webhook-message-id` (compact spec: design + code in one PR)
**Status**: drafted + implemented in the same PR
**Source**: live production evidence, `wa-burocracy`, 28/08/2026 19:07 UTC — a
signed webhook for an inbound Cafezinho voice note arrived as
`data = {id:"6252", kind:"audio", channel:"…push_name…"}`. The stanza id of
that message was `AC36…`. Nothing in the delivery carried it.

## Problem

A webhook (or SSE, or socket `subscribe`) consumer that is told "an audio
message just arrived" cannot act on it. Every message-scoped daemon method —
`media.download {messageId, transcribe:true}` (spec 110h), `reaction.send`,
`thread.get` — is addressed by the WhatsApp **stanza id**. The subscriber
projection carried only `id`, which is
`internal/adapters/secondary/whatsmeow/translate_event.go:52`'s
`domain.EventID(strconv.FormatUint(seq, 10))` — the EventBridge sequence
number. It is a stream cursor, not an address; passing it to `media.download`
resolves nothing.

The stanza id was available the whole time and was discarded at the boundary:
`translateMessage` read `evt.Info.PushName` and `evt.Info.Timestamp` off
`events.Message.Info` and never read `evt.Info.ID`
(`internal/adapters/secondary/whatsmeow/translate_event.go:200-217`,
pre-change). So `domain.MessageEvent` had no field to hold it and
`app.SubscriberMessageEvent` had no field to expose it
(`internal/app/subscriber_events.go:30-58`, pre-change).

The result is a WA agent that can be *notified* about a voice note and can
never read it. That is the concrete, reproducible defect this feature fixes —
not a speculative hardening (CLAUDE.md rule 29).

## Decision

Carry the stanza id as a **structural identifier** from the whatsmeow boundary
to the subscriber wire, alongside — never instead of — the event identity.

- `domain.MessageEvent` gains `MessageID domain.MessageID`. The existing
  `ID EventID` keeps its meaning and its name; nothing is renamed, so no
  consumer of the domain type breaks.
- `translateMessage` sets it from `evt.Info.ID`, the same `MessageInfo` struct
  it already reads `PushName` and `Timestamp` from.
- `app.SubscriberMessageEvent` gains
  ``MessageID string `json:"messageId,omitempty"` ``. That single projection is
  the choke point every push surface goes through
  (`internal/app/eventbridge.go:331` `translateDomainEvent`), so SSE, socket
  `subscribe`, `wait`, and the signed `wa.webhook/v1` envelope
  (`internal/app/webhooks.go:224` marshals `Data: evt.Payload`) all gain the
  field from one change.

**Message ids are structural, not untrusted text, so they are NOT
channel-wrapped.** This is the rule the file already states — *"Structural
identifiers (JIDs, message IDs, option IDs, kinds) stay as plain fields"*
(`internal/app/subscriber_events.go:23-25`) — and the rule two sibling fields
already follow: `targetMessageId` (reaction target) and `quotedMessageId` are
plain today. Wrapping the third would make the boundary incoherent and would
put a correlation handle somewhere no consumer can parse it. The FR-005a
envelope exists for text a human wrote; nobody writes a stanza id.

`omitempty` is deliberate: an absent key means "this producer had no stanza id"
(synthetic events; rows replayed from an `events.db` ring written before this
field existed), which is materially different from an empty id and must stay
distinguishable — CLAUDE.md rule 12, no silent fallbacks.

**"Structural identifier" is enforced, not assumed.** The sentence at
`internal/app/subscriber_events.go:23-25` claims plain ids are "validated/parsed
upstream". That is true of JIDs (they only exist via `domain.Parse`) and was
false of the message ids: whatsmeow assigns
`info.ID = types.MessageID(ag.String("id"))` (`message.go:214`) straight from the
stanza attribute, and `types.MessageID` is a bare `= string` alias
(`types/jid.go:58`) — so the *sending device* chooses every byte, and
`targetMessageId` / `quotedMessageId` were already carrying it unfiltered into
the one place the FR-005a envelope does not cover. Adding a third such field
without a guard would have widened a live prompt-injection ingress into an LLM
consumer, which is the exact audience this feature exists for.

So `domain.MessageID.IsSafe()` defines the grammar (1–64 bytes of
`[A-Za-z0-9_.:@=+/-]` — a superset of every observed WA id form: web hex,
32-char iOS hex, padded base64, business `@`/`:` forms), and one gate,
`plainMessageID`, is applied to **all three** plain id fields — the pre-existing
two included, per CLAUDE.md rule 18 (no "pre-existing" excuse) and the
fix-the-shared-function rule. A malformed id is **withheld, never echoed**, and
its field name is listed in a new `rejectedIds` array so the drop is visible
rather than silent (rule 12). Rejecting rather than quarantining in the envelope
keeps the hostile bytes off the wire entirely, and `rejectedIds` is an alertable
signal: it means a sender put something id-shaped-but-not in a structural slot.

## Surface

```jsonc
// wa.webhook/v1 delivery for an inbound voice note — `data` is the
// SubscriberMessageEvent projection, unchanged except for messageId.
{
  "schema": "wa.webhook/v1",
  "deliveryId": "whd_…",
  "topic": "message",
  "ts": 1787000000,
  "data": {
    "id": "6252",                        // EventBridge sequence — stream cursor
    "messageId": "AC3628D1F0B4A9E75C11", // NEW — WhatsApp stanza id, addressable
    "ts": 1787000000,
    "chat": "5511999999999@s.whatsapp.net",
    "sender": "5511999999999@s.whatsapp.net",
    "kind": "audio",
    "channel": "<channel source=\"wa\" …><field name=\"push_name\">…</field></channel>"
  }
}
```

The same `data` shape is what `GET /v1/events` (SSE), socket `subscribe`
notifications, and `wait` deliver. No new event kind, no new schema string, no
schema version bump: `wa.event/v1` and `wa.webhook/v1` are unchanged, because
adding an optional field is additive for every JSON consumer.

## Functional requirements

| ID | Requirement | Verifiable check |
|----|-------------|------------------|
| FR-001 | `domain.MessageEvent` carries `MessageID domain.MessageID` in addition to `ID EventID`. Neither field is renamed or removed. | Compiles; `internal/domain/event.go` field list. |
| FR-002 | `translateMessage` sets `MessageID` from `events.Message.Info.ID` on every inbound message. | `TestTranslate_MessageConversation` asserts `me.MessageID == "ABC"` while `me.ID == "1"` — distinct fixtures, so copying the wrong id fails. |
| FR-003 | `app.SubscriberMessageEvent` exposes it as `messageId`, omitted when empty. `id` keeps its existing meaning and JSON name. | `TestWrapMessageEventForSubscribers_AudioCarriesSourceMessageID`; `TestWrapMessageEventForSubscribers_NoStanzaIDOmitsMessageID`. |
| FR-004 | The signed `wa.webhook/v1` delivery body for an audio message carries `data.messageId`. | `TestWebhookFanOutCarriesSourceMessageID` drives the real chain: `domain.MessageEvent` → `translateDomainEvent` → `WebhookWorker.fanOut` → enqueued payload JSON. |
| FR-005 | The prompt-injection firewall is unchanged: no body, caption, path or push name appears outside `data.channel`, and the audio envelope still carries no text field. | Same two tests assert the forbidden-key set on the marshalled wire form. |
| FR-006a | `domain.MessageID.IsSafe()` accepts every observed real stanza-id form and rejects empty, over-length, whitespace, control, quote, angle-bracket and non-ASCII bytes. | `TestMessageIDIsSafe`, 16 rows. |
| FR-006b | All three plain id fields (`messageId`, `targetMessageId`, `quotedMessageId`) pass the same gate. A malformed id is withheld, its bytes never appear anywhere on the wire, and its field name is listed in `rejectedIds`. Well-formed ids are never altered and produce no `rejectedIds` key. | `TestWrapMessageEventForSubscribers_HostileIDsAreWithheld`; `TestWrapMessageEventForSubscribers_WellFormedIDsAreNotRejected`. |
| FR-006 | Existing projections are behaviourally unchanged — text, media, reaction, quote, interactive, nil-payload, and every `Kind` mapping. | The pre-existing `subscriber_events_test.go` suite passes unmodified. |

## Alternatives rejected

Per CLAUDE.md rule 20.

### A. Replace `id` with the stanza id

Make `SubscriberMessageEvent.ID` hold `Info.ID` and drop the sequence number.

**Rejected**: `id` is load-bearing as a stream cursor. `GET /v1/events` frames
use it for `Last-Event-ID` gapless resume, and the durable ring keys on the
sequence. Overloading one field with two identities would break resume for
every existing subscriber and silently change the meaning of a field with no
version bump — the exact wire break a `/v1` schema promises not to make.

### B. Channel-wrap the stanza id

Put `messageId` inside the FR-005a `<channel>` envelope on the grounds that it
originates on the sender's device.

**Rejected**: the envelope is a *presentation* boundary for text an LLM will
read as prose; a consumer cannot machine-parse a correlation handle out of it,
which defeats the entire purpose of surfacing it. It would also contradict the
two ids the same struct already exposes plain (`targetMessageId`,
`quotedMessageId`) and the file's own stated rule.

The underlying worry — that the id is attacker-chosen — is real and is answered
instead by `IsSafe` + `plainMessageID` above: withhold what is not an id rather
than relocate it. Relocation would still print attacker prose to the consumer,
only escaped; withholding prints nothing and names the field in `rejectedIds`.

Out-of-scope neighbour, named so it is not forgotten: `wireMessage.messageId`
(`cmd/wad/history_methods.go:303`) reads from the persisted `messages` table
rather than the live stanza, so it is a different producer and a different
trust path. It is not covered by this gate.

### C. Derive the stanza id downstream from `chat` + `ts`

Have consumers call `thread.get` and match on timestamp.

**Rejected**: racy (two messages can share a second), costs an extra RPC per
event, and is unavailable to the webhook consumers this exists for — they hold
a signed POST body, not a socket.

## Test plan

- `TestWrapMessageEventForSubscribers_AudioCarriesSourceMessageID` — held-out
  gate: audio event carries BOTH `id` and `messageId`, distinct; envelope stays
  body-free; wire form leaks no raw text key.
- `TestWrapMessageEventForSubscribers_NoStanzaIDOmitsMessageID` — absent id
  omits the key rather than emitting `""`.
- `TestWebhookFanOutCarriesSourceMessageID` — signed-delivery end of the chain.
- `TestTranslate_MessageConversation` (extended) — the adapter reads `Info.ID`.
- `TestMessageIDIsSafe` — 16 rows, accept and reject halves both populated.
- `TestWrapMessageEventForSubscribers_HostileIDsAreWithheld` — all three plain
  id fields gated; hostile bytes absent from the marshalled wire form.
- `TestWrapMessageEventForSubscribers_WellFormedIDsAreNotRejected` — the
  over-tight-validation failure mode, which would break real addressing.
- Full suite `go test -race -shuffle=on -count=1 ./...` + `golangci-lint run`
  + `nix flake check`.
- Production verification: canary `wa-burocracy`, receive one voice note, read
  the receiver's stored delivery **metadata only** (`data.messageId`,
  `data.kind`, `data.id`) — never the transcript or body.

## Out of scope

- Any n8n / flow / consumer change. Another track owns those.
- Auto-transcription of inbound audio (spec 110h's inbound follow-up).
- `domain.MediaTranscribedEvent` marshalling its fields as `MessageID` rather
  than `messageId` — a real inconsistency with spec 110h's documented surface,
  but a different event, a different wire break, and not this defect.
- `wireMessage.messageId` on the history/thread read path — a different
  producer (persisted rows, not the live stanza).

## Success criteria

| Criterion | Metric |
|-----------|--------|
| SC-001 | A signed `wa.webhook/v1` delivery for an inbound voice note contains a non-empty `data.messageId`. |
| SC-002 | That value is accepted by `media.download {messageId, transcribe:true}` — i.e. it is the address, not a cursor. |
| SC-003 | `data.id` still equals the EventBridge sequence number, and SSE `Last-Event-ID` resume is unaffected. |
| SC-004 | No sender-authored text appears outside `data.channel` in any delivery, and a malformed id never appears at all. |
| SC-005 | `go test -race -shuffle=on -count=1 ./...`, `golangci-lint run`, and `nix flake check` are green. |
