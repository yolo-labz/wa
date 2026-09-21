# Reproduce the CLI demo

```bash
nix develop -c ./scripts/record-demo.sh
nix develop -c bash scripts/check-demo.sh
python3 scripts/test_record_demo.py
```

The pinned dev shell provides Go, VHS, FFmpeg and jq; Python 3 is needed only
for the small ownership regression. The first shell entry may build its Go
pin. No account, pairing, browser profile or running service is needed.

## What is shown

- `wa --version`: the source tree's `git describe --tags --always --dirty`, not
  just the nearest release tag (`scripts/record-demo.sh:42`).
- `wa doctor`: genuine local diagnostics, including **device not paired** and
  any fresh-sandbox warnings. Its socket check is not proof of inbound delivery;
  the freshness rows are informational placeholders (`cmd/wa/cmd_doctor.go:101`,
  `cmd/wa/cmd_doctor.go:316`).
- `wa status`: disconnected/unpaired status, not a successful connection.
- `wa allow list`: an empty allowlist. Authorization is default-deny; this
  recording does not attempt a send or prove enforcement against WhatsApp.
- `wa msg revoke --help`: documents self/everyone scope and **Neither is
  reversible** (`cmd/wa/cmd_msg.go:26`). It never executes revoke.

Ordinary sends have non-overridable 2/second and 30/minute windows and a fresh
session warmup ramp; see the [documented safety controls](../../CLAUDE.md#safety-build-the-brakes-first-not-after-the-first-ban).
These are documentation, **not exercised sends**. This demo does not pair,
read history, add allowlist entries, mutate a real message, or control a service.

## Isolation and concurrency

The helper acquires `docs/assets/.wa-demo.lock` atomically and refuses a second
recorder in the same checkout. It creates a private short `/tmp/wa-demo.XXXXXX`
root with `mktemp`; it never deletes the old fixed `/tmp/wa-demo-env` path.
Separate checkouts can record independently. HOME, all XDG roots and TMPDIR are
isolated, and `env -i` removes inherited WA/remote and shell-startup settings.
An unpaired adapter stays idle until pairing (`internal/adapters/secondary/whatsmeow/adapter.go:408`).

Normal exit, failed builds, INT and TERM clean up only the acquired resources.
SIGKILL cannot run a shell trap: **inspect the lock and recording process** before
manually removing a stale lock; the helper intentionally does not steal it.
Assets are staged and checked before copying them under the output lock. This
is not a crash-atomic transaction across five files: an interrupted publication
can be repaired by rerunning after confirming ownership of any leftover lock.

## Accessibility, provenance and checks

[PNG](wa-demo.png) is a static doctor frame; [text](wa-demo.txt) is actual output
captured from the same unpaired process (not a drawn terminal). [MP4](wa-demo.mp4)
and [WebM](wa-demo.webm) support player-controlled playback. The [GIF](wa-demo.gif)
is the README preview. All are 1100×640 and silent, with 1× playback. The tape
requests 15 fps capture; these encoded outputs measure 25 fps in FFprobe.
Typed commands and pauses are scripted by [VHS](wa-demo.tape); runtime
output is not edited. Random paths, timings and build identifiers vary, so
reproduction is semantic rather than byte-identical.

The metadata check requires 10–60 second animations, no audio, exact dimensions,
GIF ≤2 MiB, each video ≤5 MiB, PNG ≤1 MiB and key text from the actual commands.
It does not replace visual inspection. Evidence, frame samples and checksums
are in [wa-demo-evidence/](wa-demo-evidence/); the canonical implementation
report is [swarm-2026-09-21.md](../swarm-2026-09-21.md).

Content and scripts use the repository's Apache-2.0 license. No music, third-party
account data, external stock footage or synthetic successful CLI output is used.
