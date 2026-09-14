# Chat-scoped media and historical quotes

Candidate for issue #383; not deployed. Checkpoint: 14/09/2026.

Hypothesis: stanza IDs are unique per chat, not globally; selecting a global
first match can attach another chat's bytes even when metadata was filtered.

## Contract

`media.download` accepts optional `chat`. When supplied, it is parsed as a JID;
`messageId` follows `domain.MessageID.IsSafe` (1–64 ASCII identifier characters).
The selected `(chat, messageId)` determines the proto before cache lookup,
download or transcription. A qualified miss never retries globally.

Every successful download returns its actual selection alongside `object`:

```json
{"selection":{"chatJid":"123@s.whatsapp.net","messageId":"ABC"}}
```

For qualified requests, reject absent or mismatched selection before using any
SHA/path/transcript. Older daemons ignore unknown parameters: sending `chat`
alone is not proof. The new CLI enforces this binding for `download` and
`fetch --message-id`, exiting 78 before fetching/writing bytes on mismatch.
A SHA-only fetch is content-addressed, not chat-qualified.

```sh
wa media download --chat 123@s.whatsapp.net --message-id ABC --json
wa media fetch --chat 123@s.whatsapp.net --message-id ABC --out image.bin
```

Unqualified compatibility calls require exactly one stored match. Duplicate IDs
return typed `message_id_ambiguous` before cache/download/transcription, regardless
of insertion order or proto validity. Supply the intended chat; this is not an
expired-media condition and does not call for resync. Existing unqualified MCP
voice transcription keeps its schema and inherits ambiguity refusal.

`media.list` retains each row's chat during proto inspection. The quoted-message
adapter for existing list/button replies likewise retains the destination chat;
no outbound capability or allowlist is widened.

History/export and the shared history projections emit optional validated
`quotedMessageId` only when the referenced row exists in that chat. Missing,
unsafe or cross-chat-only targets omit the field and list `quotedMessageId` in
`rejectedIds`. Legacy/corrupt proto or no quote yields no quoted ID. The target's
existence is metadata proof, not a promise its media is still downloadable.
Inbound text stays wrapped; raw proto is never serialized to the client.

## Validation and release status

Synthetic tests cover both duplicate insertion orders, distinct fake hashes and
payloads, cold/warm cache, missing/legacy/corrupt selected rows, scoped inspection,
CLI/RPC forwarding, old-daemon rejection before byte fetch, and history/export
photo/sticker/no-quote/rejected references. No real account or private media is
used. Run:

```sh
go test -race -shuffle=on -count=1 ./...
go build ./cmd/wa ./cmd/wad
go vet ./...
golangci-lint run ./...
```

Use a short owned TMPDIR for Unix-socket fixtures. Mode-precondition tests need
normal subprocess umask 022; changing only that test subprocess does not change
service permissions. Keep evidence files private separately.

Generator family: OpenAI (bounded Sol implementation plus parent integration).
This document is not independent approval or a Speckit workflow receipt. The
worker timed out during validation; parent checks/results are separately retained
under `~/.local/state/wa-media-scope-385/`. No merge/deploy/resync/replay/send/delete,
runner change, database migration or pairing operation is included. Release still
needs exact-head independent review and coordinated wad/CLI/consumer rollout.
Until then issue #383 remains open. Revert through a PR if this candidate lands;
roll back dependent consumers before restoring an older daemon/CLI.
