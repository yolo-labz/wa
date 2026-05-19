# Feature 110e — Quickstart: `wa pair --remote`

30-second guide for operators. Assumes `wa` is installed locally and the operator has SSH access to the dokku host that runs the target daemon.

## Three invocations

### 1. QR in terminal

```bash
wa pair --remote ProxMox.Dokku:wa-burocracy
```

Output (truncated, half-block QR):

```
█████  ▄▄▄▄▄  █▄ █▄█ ▄▄▄▄▄  █████
█ ▄▄▄ █ ▀█▀▀▄ ▀▄▄ █  █ ▄▄▄ █ ▀▄ █
█ ███ █ █▀▄▀█ ▀▀█▀▄  █ ███ █ ▀█▄
...
Scan with WhatsApp → Settings → Linked Devices → Link a Device.
```

WhatsApp captures the QR. Daemon receives `events.PairSuccess`. Command exits 0.

### 2. QR in local browser

```bash
wa pair --remote ProxMox.Dokku:wa-burocracy --browser
```

QR renders in operator's terminal (same as above), AND `--browser` is forwarded to the in-container `wa pair`. (See note in `contracts/cli-flag.md` — browser-side HTML auto-open is operator-local; cross-host HTML forwarding is out of scope for 110e.)

### 3. Phone-code (no QR)

```bash
wa pair --remote ProxMox.Dokku:wa-burocracy --phone +5511999999999
```

Output:

```
Pair code: ABCD-EFGH
Open WhatsApp → Settings → Linked Devices → Link with phone number.
Enter the code above within 60 seconds.
```

## Output examples

### Successful pair

```
Pairing started. Scan the QR.
█████  ▄▄▄▄▄  █▄ █▄█ ▄▄▄▄▄  █████
...
{"schema":"wa.pair/v1","state":"success","jid":"558191100082:26@s.whatsapp.net"}
```

Exit 0.

### Malformed `--remote` value

```bash
wa pair --remote not-a-valid-shape
```

```
wa pair --remote: expected <host>:<app>, got missing ':' separator.
Example: --remote ProxMox.Dokku:wa-burocracy
```

Exit 64.

### URL form refused

```bash
wa pair --remote https://wa-burocracy.home301server.com.br
```

```
wa pair --remote: pair requires SSH access to the daemon's host, not the REST endpoint.
Use --remote <ssh-host>:<dokku-app> instead — e.g. --remote ProxMox.Dokku:wa-burocracy.
```

Exit 64.

## Troubleshooting

### SSH key not loaded

`ssh` prompts for password or exits with `Permission denied (publickey)`. Load your key:

```bash
ssh-add ~/.ssh/id_ed25519
wa pair --remote ProxMox.Dokku:wa-burocracy
```

### Dokku app does not exist on host

```
 !     App 'wa-typo' does not exist.
```

Verify with `ssh ProxMox.Dokku dokku apps:list` and correct the `<app>` portion of `--remote`.

### Daemon already paired

```
{"schema":"wa.pair/v1","state":"error","error":"already paired"}
```

Wipe first (destructive — clears messages.db + audit.log), then re-pair:

```bash
ssh -t ProxMox.Dokku 'dokku enter wa-burocracy -- /usr/local/bin/wa panic'
wa pair --remote ProxMox.Dokku:wa-burocracy
```

Less destructive (preserves messages, only un-links server-side):

```bash
ssh -t ProxMox.Dokku 'dokku enter wa-burocracy -- /usr/local/bin/wa session logout-all'
wa pair --remote ProxMox.Dokku:wa-burocracy
```

(`wa panic --remote` and `wa session logout-all --remote` are out of scope for 110e. Follow-up specs 110f/110g.)

## Setup checklist (one-time per host)

1. SSH key for operator's workstation in `~dokku/.ssh/authorized_keys` on the dokku host (or use a normal user with `sudo dokku` access).
2. Dokku app exists: `ssh dokku-host dokku apps:list | grep wa-`.
3. Local `wa` binary is at or newer than the version that shipped 110e (verify with `wa version`).

## Reference

- `spec.md` — full feature spec.
- `contracts/cli-flag.md` — flag semantics + interaction matrix.
- `docs/deploy/dokku.md` — broader dokku ops runbook (updated by P4 of plan).
