# Spec 110j — outbound interactive reply (relax FR-131 reply-class)

Closes #150.

## Problem

`wa` agents cannot complete real-world WhatsApp Business workflows that gate the human queue behind interactive-list IVRs (Zenvia / Wati / Twilio / Meta Cloud API templates). Concrete case: Clínica Mediar (`558134658209@s.whatsapp.net`, business LID `104457933209684@lid`) replies to scheduling enquiries with a single-select list whose options carry opaque `SelectedRowID` values. Plain-text input — including the literal `"1"`, the row title `"Unidade Recife - Rio Mar"`, `"atendente"`, `"Humano"` — returns the canned `não encontrei a opção digitada` error. Only `Encerrar` (close-session) is recognised. The bot will never escalate to a human atendente without a valid `ListResponseMessage.SingleSelectReply.SelectedRowID`.

FR-131 of spec 017 currently forbids ALL outbound interactive messages (`ListMessage`, `ButtonsMessage`, `ListResponseMessage`, `ButtonsResponseMessage`, `TemplateButtonReplyMessage`). The rationale at the time was anti-abuse: agents emitting unsolicited menus could be reported as spam. That rationale does not apply to REPLIES — `ListResponseMessage` / `ButtonsResponseMessage` / `TemplateButtonReplyMessage` only echo an `id` the peer offered first; WhatsApp's official clients enforce the same shape.

This blocker hits ~every Brazilian clinic / cartório / órgão público WhatsApp Business funnel. Without this fix, the operator falls back to the phone app, defeating the daemon-first automation model that drives the entire `wa-assistant` plugin.

## Functional requirements

- **FR-001**: A new domain variant `domain.ListReplyMessage{Recipient JID, RowID string, Title string}` MUST satisfy `domain.Message`. `Validate()` MUST reject zero recipient and empty `RowID`. Title is optional (carries the human-readable label for audit log + history persistence; the wire-level proto echoes it back via `ListResponseMessage.Title`).
- **FR-002**: A new domain variant `domain.ButtonReplyMessage{Recipient JID, ButtonID string, DisplayText string, Kind ButtonReplyKind}` MUST satisfy `domain.Message`. `Kind` is a closed enum (`ButtonReplyButtons`, `ButtonReplyTemplate`) selecting which proto field gets populated. `Validate()` MUST reject zero recipient and empty `ButtonID`. `DisplayText` is optional (omitted for templates).
- **FR-003**: Adapter `internal/adapters/secondary/whatsmeow/buildOutboundMessage` MUST map the new variants to the matching `waE2E` proto:
  - `ListReplyMessage` → `*waE2E.ListResponseMessage{Title, SingleSelectReply: {SelectedRowID}, ListType: SINGLE_SELECT}`
  - `ButtonReplyMessage{Kind=Buttons}` → `*waE2E.ButtonsResponseMessage{SelectedButtonID, SelectedDisplayText, Type: DISPLAY_TEXT}`
  - `ButtonReplyMessage{Kind=Template}` → `*waE2E.TemplateButtonReplyMessage{SelectedID, SelectedDisplayText}`
- **FR-004**: Two new JSON-RPC methods on the dispatcher:
  - `send.listResponse` — params `{to: jid, rowId: string, title?: string, idempotencyKey?: string}` — result `{messageId, timestamp}` (same shape as `send`).
  - `send.buttonResponse` — params `{to: jid, buttonId: string, displayText?: string, kind?: "button"|"templateButton", idempotencyKey?: string}` — result `{messageId, timestamp}`. `kind` defaults to `"button"` when omitted.
  Both MUST flow through the existing `idempotentCall` wrapper (FR-034a replay semantics), the existing safety pipeline (`checkSafetyAndAudit` with `domain.ActionSend`), and the existing block-list check (`ensureNotBlocked`). No new allowlist scope.
- **FR-005**: CLI surface — extend `wa send` with three mutually-exclusive flags:
  - `--list-row-id <id>` [+ optional `--list-row-title <title>`]
  - `--button-id <id>` [+ optional `--button-display-text <label>`]
  - `--template-button-id <id>` [+ optional `--button-display-text <label>`]
  Each is mutually exclusive with `--body` and with each of the others. Existing `--to`, `--idempotency-key`, `--remote`, `--profile`, `--json` semantics unchanged. Validation failure exits with `64` (usage) and a single-line stderr message naming the conflicting flags.
- **FR-006**: FR-131 of spec 017 MUST be SPLIT, not removed:
  - **Unsolicited outbound interactive** (`ListMessage`, `ButtonsMessage`, `InteractiveMessage`, `HighlyStructuredMessage`, `TemplateMessage` carrying button definitions) STAYS forbidden. The "intentionally do NOT" comment in `translate_interactive.go` referring to those proto types stays.
  - **Reply outbound interactive** (`ListResponseMessage`, `ButtonsResponseMessage`, `TemplateButtonReplyMessage`) is permitted under the existing `send` allowlist scope. This is the new permitted surface.
- **FR-007**: Audit log entries for the new methods MUST use action `domain.AuditSend` with a `detail` field of `list_response:<rowID>` / `button_response:<buttonID>` so a `grep` of `audit.log` can attribute the outbound choice without parsing the raw RPC.

## Non-functional

- No new Go dependencies.
- No new external secondary adapter — the existing `whatsmeow.Adapter` adds three constructor helpers in the same package as `send.go`.
- No JSON-RPC schema version bump. Adding new methods to a JSON-RPC server is additive; existing clients that never call them are unaffected.
- CGO_ENABLED=0 invariant holds.
- Forward compat: a future `wad` build that drops these methods would surface as `-32601 method not found` to old clients — same as any other method we deprecate. No protocol-version handshake change.
- Inbound translator (`extractInteractive`) is unchanged. The send path is the only delta.

## Rejected alternatives

1. **Wholesale lift of FR-131.** Allow unsolicited list/button sends too. Rejected — re-opens the spam vector that motivated FR-131 in the first place; the reply-class restriction matches what WhatsApp's own client enforces.
2. **Repurpose `send.reply` with an `interactive` discriminator.** Rejected — `send.reply` is for quoted text replies (FR-070..FR-073); overloading it with a different proto and a different param schema would force every existing `send.reply` caller to re-validate against a sum-type schema. New method per behaviour is the lower-risk Go-CLI idiom.
3. **Pass `selectedRowID` as a new optional field on `send` itself.** Rejected — `send` currently requires `body` and is reused by every consumer; making `body` mutually exclusive with `selectedRowID` at the JSON layer pushes a validation forest into a single handler. Two new methods keep schemas narrow.
4. **Native flow responses (`Message_InteractiveResponseMessage`).** Out of scope. Carry structured form data (`responseFormat: "Json"`), separate proto, separate UX. Future work — file a follow-up issue if a real-world bot demands it.
5. **Skip the `Kind` enum and have two domain types `ButtonReplyMessage` + `TemplateButtonReplyMessage`.** Rejected — adapter switch already pays the cost; one type + one closed enum keeps the domain sealed sum-type set from sprawling.

## Test plan

- `internal/domain/message_test.go`: extend with `ListReplyMessage` and `ButtonReplyMessage` validation cases (zero JID rejected; empty rowID/buttonID rejected; valid happy path; sealed sum-type marker enforced via `isMessage()`).
- `internal/adapters/secondary/whatsmeow/send_test.go` (new — there is no existing whatsmeow send test that hits the proto layer): unit test `buildOutboundMessage` for each new variant. Assert the resulting `*waE2E.Message` carries the expected nested proto with `SelectedRowID` / `SelectedButtonID` populated. **No** live whatsmeow client — use a fake or a direct assertion on the build helper.
- `internal/adapters/secondary/whatsmeow/translate_interactive_roundtrip_test.go` (new): build outbound → fake-inbound (feed the resulting `*waE2E.Message` back through `extractInteractive`) → assert the resulting `domain.InteractivePayload.Options[0].ID` matches the input `RowID` / `ButtonID`. Catches proto-field-name drift across whatsmeow bumps.
- `internal/app/method_send_interactive_test.go` (new): unit test both new dispatcher methods against an in-memory `MessageSender` fake. Cover: happy path → returns `messageId`; allowlist deny → `-32100`; rate-limit deny → `-32200`; block-list deny → recorded audit; missing `rowId` → `-32602 invalid params`.
- `cmd/wa/cmd_send_interactive_test.go` (new): mutex-flag matrix (`--body` + `--list-row-id` → exit 64; `--list-row-id` + `--button-id` → exit 64; `--list-row-id` alone → success; `--button-id` alone → success; `--template-button-id` alone → success).
- `internal/app/porttest/`: NO new contract test — `MessageSender.Send` is the unchanged port; the new domain variants flow through it without expanding the port surface.

## Implementation

Files touched:

- `internal/domain/message.go` — add `ListReplyMessage`, `ButtonReplyMessage`, `ButtonReplyKind` enum. ~50 lines.
- `internal/adapters/secondary/whatsmeow/send.go` — extend `buildOutboundMessage` switch; new `buildListResponse` / `buildButtonsResponse` / `buildTemplateButtonResponse` helpers. ~40 lines.
- `internal/app/method_send_interactive.go` (new) — `handleSendListResponse` / `handleSendButtonResponse`. Mirrors `method_send.go` shape. ~120 lines.
- `internal/app/dispatcher.go` — register the two new methods in the method map. 2 lines.
- `cmd/wa/cmd_send.go` — add the three new flag pairs + mutex validation. ~50 lines.
- `internal/adapters/secondary/whatsmeow/translate_interactive.go` — update the "intentionally do NOT" comment to reflect the FR-131 split. 2 lines.

LoC budget: ≤280 lines source + ≤220 lines test.

## Forward compat

If `wad` is downgraded to a version that predates these methods, clients calling `send.listResponse` / `send.buttonResponse` receive `-32601 method not found`. CLI surface (`wa send --list-row-id …`) translates this into exit 78 (config error) with a stderr hint to upgrade the daemon — pattern reused from spec 110i FR-003.

If the inbound bot stops offering an interactive prompt mid-flow, the operator can fall back to plain `--body` text. Nothing in this spec changes the existing `send` behaviour.

## Cloud-API peers — operator workaround taxonomy

The shipped reply-class send path (#161 → #163 → #165) is wire-shape complete
against plain WhatsApp peers, but the server still rejects with stanza error
`479` when the inbound interactive originated from a **WhatsApp Business Cloud
API** account (Zenvia / Wati / Twilio / 360dialog / Meta-hosted). This is an
architectural limit upstream, not a wa code bug — see issue #167 for the
full diagnosis (Cloud API strips `MessageContextInfo.MessageSecret` before
forwarding to linked-device clients, so the companion has no secret to bind
a `list_reply` back to).

This section documents the operator-facing fallbacks in priority order. None
of them require code changes in wa.

### Identifying a Cloud-API peer

The smoking-gun shape:

- Inbound message's sender JID carries a `@lid` alias (Hidden User Server)
- The chat is a WhatsApp Business account (verified badge or business profile)
- The interactive menu reaches the wa daemon decoded fine (no decryption error
  on the inbound path)
- The outbound `list_reply` to that same chat returns stanza `479`

If all four hold, the peer is Cloud-API-coexistent and the workarounds below
apply. If only the first three, retry — `479` is also returned for transient
session-cache misses (whatsmeow #960).

### Fallback 1 — Plain-text reply matching the row title (cheapest)

Many Business IVRs accept the exact label of the menu option as plain text:

```bash
wa send --to <business-jid> --body "Unidade Recife - Rio Mar"
```

If the bot's NLU matches on the row title (most do), this completes the flow
with zero key-material involvement. Costs one extra round-trip if the bot
echoes a confirmation. Try this first — it works for ~60% of Cloud-API peers
observed in production (Mediar, Tagme, generic Zenvia bots).

### Fallback 2 — Quote-reply via `send.reply`

If the bot rejects free-text and demands the structured selection, fall back
to `send.reply`. This sends an `ExtendedTextMessage` with
`ContextInfo.StanzaID` referencing the menu, bypassing the
`interactive.list_reply` channel entirely:

```bash
wa send reply --to <business-jid> \
  --quoted-id <inbound-stanza-id> \
  --body "Unidade Recife - Rio Mar"
```

Whether the peer routes the quoted text as a list-pick depends on the peer's
bot logic. Some Cloud-API bots are configured to interpret a `quotedMessage`
context as a structured response; others are not. Empirical only — try and
see.

### Fallback 3 — Primary-phone proxy (manual)

The operator opens WhatsApp on the primary phone holding the account and
taps the menu option on the native client. The native app carries the full
key material because it IS the primary — Cloud-API stripping doesn't apply.
The wa daemon receives the bot's next message a few seconds later via the
normal Coexistence sync path.

Operationally: one-off only. Not automatable.

### Fallback 4 — Partner webhook (out-of-band)

If the business operator (Mediar-class, Tagme-class) is reachable directly,
ask them to add a Cloud-API webhook integration on their side. Sidesteps
WhatsApp entirely. Highest reliability, requires partner cooperation.

### Anti-pattern — companion-device pairing

Adding another linked device (a Chromium WA Web session against the same
account, or a second wa daemon) does **not** help. Both companions share
the same identity material and pull from the same Cloud-API-stripped feed.
The reason WA Web's mobile/desktop UI works against the same business IVR
is that the primary phone has the original Cloud-API-issued message with
`MessageContextInfo.MessageSecret` intact; WA Web pulls key material from
the phone over the encrypted local channel. A wa daemon paired without a
backing primary phone has no source for the missing secret.

The companion-pair primitive is still useful for non-Cloud-API peers
(operator preview, multi-tab UX), but it is not a fix for this specific
class.

### Empirical confirmation (for diagnosis, not workaround)

To prove a given `479` is a Cloud-API peer (vs a transient session miss),
bump the daemon to debug:

```bash
ssh ProxMox.Dokku 'dokku config:set wa-burocracy WA_LOG_LEVEL=debug'
# trigger a fresh menu cycle:
wa --remote "$HOST" send --to <business-jid> --body "Olá"
# capture stderr after the bot's response:
ssh ProxMox.Dokku 'docker logs --since 60s $(docker ps -qf name=wa-burocracy.web.1) 2>&1 \
  | grep -iE "messageSecret|deviceList|listMessage|encryptMessageForDevice"'
ssh ProxMox.Dokku 'dokku config:unset wa-burocracy WA_LOG_LEVEL'
```

The trace should show `MessageContextInfo` nil or `MessageSecret` empty, and
the outbound encryption failing with `ErrNoSession` (whatsmeow #960
signature). If both signals are present, the peer is Cloud-API and code
fixes will not help — route to fallback 1.

### Long-term fix — out of scope here

The only fully-automatable path that closes the gap is running the official
WhatsApp Android client inside an emulator under the operator's control and
scripting it via `adb` / `mobile-mcp`. The emulator IS the primary, so it
carries the full key material and emits real `list_reply` payloads.
Operationally heavy. File a separate spec if demand surfaces.

## References (Cloud-API peers section)

- Issue #167 — full architectural diagnosis with whatsmeow / Baileys upstream evidence
- Baileys #1686 — Cloud API Coexistence strips messageContextInfo / deviceListMetadata
- whatsmeow #960 — hosted/coexistent 479 regression (`ErrNoSession` on `encryptMessageForDevice`)
- Meta WhatsApp Cloud API — Interactive List Messages documentation
