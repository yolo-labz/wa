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
`quotedMessageId`) and the file's own stated rule. If sender-supplied id
*hygiene* is ever wanted, it is one shared validation across all three fields
plus `wireMessage.messageId` — a different change, deliberately not smuggled
into this one (rule 13).

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
- Validation/normalisation of sender-supplied ids (see rejected alternative B).

## Success criteria

| Criterion | Metric |
|-----------|--------|
| SC-001 | A signed `wa.webhook/v1` delivery for an inbound voice note contains a non-empty `data.messageId`. |
| SC-002 | That value is accepted by `media.download {messageId, transcribe:true}` — i.e. it is the address, not a cursor. |
| SC-003 | `data.id` still equals the EventBridge sequence number, and SSE `Last-Event-ID` resume is unaffected. |
| SC-004 | No sender-authored text appears outside `data.channel` in any delivery. |
| SC-005 | `go test -race -shuffle=on -count=1 ./...`, `golangci-lint run`, and `nix flake check` are green. |
