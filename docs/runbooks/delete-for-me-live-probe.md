# Runbook: prove `message.revoke scope=self` on a live session

Feature 115 makes `scope=self` push a `deleteMessageForMe` app-state mutation.
The unit tests pin the patch this daemon builds; they cannot pin what
WhatsApp's servers and your phone do with it. **Until this probe has been run
against a given deployment, treat delete-for-me as implemented-but-unproven**
(CLAUDE.md rule 29 — status ◐, not ✓).

## What could still be wrong after green tests

The mutation index is an addressing key. A wrong key is not rejected — the
server stores it and no client ever matches it, so the call returns `{}` and
nothing disappears. That failure is invisible from the daemon side, which is
exactly why it needs a human looking at a phone.

The two parts most likely to be wrong, in order:

1. **The fifth index element (participant).** whatsmeow's `BuildStar` passes
   the third-party sender's JID; Baileys hardcodes `"0"` for the same index.
   We follow whatsmeow. If step 5 below works in a 1:1 chat but fails in a
   group, this is the reason — flip `buildDeleteForMe` to always emit `"0"`
   and re-probe.
2. **`Version: 3`.** Taken from Baileys `chat-utils.ts`, not from whatsmeow
   (which ships no builder for this index). If step 4 fails in a 1:1 chat too,
   suspect the version before anything else.

## Probe

Run against your OWN self-chat. Nothing here touches another person: a
delete-for-me is never visible to a peer, and the message being deleted is one
you just sent to yourself. It is still **irreversible** — nothing in the
protocol undoes it — so do not point step 3 at a message you want to keep.

1. Confirm the daemon is paired and connected:

   ```bash
   wa --remote "$WA_REMOTE" status
   ```

2. Send a throwaway message to your own self-chat. Use the JID that currently
   carries text — WhatsApp moved self-chats to `<digits>@lid` during 2026, and
   the phone-number JID may still exist while receiving only empty protocol
   rows. `wa chat list` shows which one has real bodies.

   ```bash
   SELF=<your-self-chat-jid>
   wa --remote "$WA_REMOTE" send --to "$SELF" \
     --body "delete-for-me probe $(date -Is)" \
     --idempotency-key "$(uuidgen)"
   ```

3. Read the id back, and keep it:

   ```bash
   MID=$(wa --remote "$WA_REMOTE" history --chat "$SELF" --limit 1 --json | jq -r .messageId)
   echo "$MID"
   ```

4. Look at the phone. The message is there. Now revoke it for yourself:

   ```bash
   wa --remote "$WA_REMOTE" msg revoke --chat "$SELF" --messageId "$MID" --scope self
   ```

5. **Look at the phone again.** This is the assertion; the RPC returning `{}`
   is not.

   - Message gone from the phone → delete-for-me works. Record the probe date
     and the build in the deployment's notes.
   - Message still on the phone → the patch was accepted and ignored. Read
     "what could still be wrong" above. Do NOT mark the feature verified.

6. Repeat steps 2–5 in a **group**, deleting a message sent by someone else,
   to exercise the participant element. A 1:1 pass does not cover FR-115-6.

## Reverting

The feature is one branch of one method. To fall back to the pre-115
behaviour without reverting the whole PR, make `Revoke` skip the
`domain.RevokeSelf` case in `internal/adapters/secondary/whatsmeow/moderator.go`
— but note that the old behaviour was reporting success for a delete that
never happened, so a plain `git revert` of the feature commit is usually the
more honest rollback.
