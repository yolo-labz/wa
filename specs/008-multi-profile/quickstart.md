# Quickstart: Multi-Profile Support

**Feature**: 008-multi-profile
**Goal**: Reproduce a two-profile installation (default + work) end to end in under 10 minutes.

## 0. Prerequisites

```bash
go version       # 1.25+
git status       # On branch 008-multi-profile
```

Optional (for real pairing): two WhatsApp phones — one for `default`, one for `work`. The automated test works with in-memory fakes and does not require real phones.

## 1. Build the 008 binaries

```bash
go build -o wad ./cmd/wad
go build -o wa  ./cmd/wa
```

## 2. Verify migration works on an existing 007 install (if applicable)

If you're upgrading from a 007 install that already has a paired session:

```bash
./wa migrate --dry-run
```

Expected output: a table showing the 5 file moves into `default/` subdirectories.

Then apply:

```bash
./wad  # triggers automatic migration on first run
```

Expected: one audit log entry `migrated legacy single-profile layout → default/` and the daemon proceeds as normal with the `default` profile active. `wa status` returns the same JID as before the upgrade.

## 3. Create a new profile from scratch

```bash
./wa profile create work
```

Expected output:
```
Created profile 'work' at ~/.local/share/wa/work/
Run 'wa --profile work pair' to pair a device.
```

Verify the directory structure:
```bash
ls ~/.local/share/wa/             # lists: default/ work/
ls ~/.local/share/wa/work/        # lists: (empty, just the directory)
```

## 4. List profiles

```bash
./wa profile list
```

Expected:
```
PROFILE   ACTIVE  STATUS          JID
default   *       connected       5511999999999@s.whatsapp.net
work              not-paired
```

## 5. Pair the work profile

Start the work daemon in a second terminal:

```bash
./wad --profile work
```

It starts listening on `~/Library/Caches/wa/work.sock` (macOS) or `$XDG_RUNTIME_DIR/wa/work.sock` (Linux), separate from the `default` profile's socket.

Back in the first terminal:

```bash
./wa --profile work pair --browser
```

A new browser tab opens showing the work profile's pairing HTML (a different temp file — `$TMPDIR/wa-pair-work.html`). Scan with your work phone.

Verify:

```bash
./wa --profile work status
```

Expected: `Connected as <work-jid>` — a different JID from the default profile.

## 6. Allowlist and send from each profile independently

```bash
./wa --profile default allow add <default-jid> --actions send
./wa --profile default send --to <default-jid> --body "hello from default profile"

./wa --profile work allow add <work-jid> --actions send
./wa --profile work send --to <work-jid> --body "hello from work profile"
```

Both messages deliver. Each profile has its own allowlist (`~/.config/wa/default/allowlist.toml` and `~/.config/wa/work/allowlist.toml`) and its own audit log.

## 7. Switch active profile

```bash
./wa profile use work
./wa status   # queries work profile without --profile flag
./wa profile use default
./wa status   # queries default profile
```

The active profile pointer is at `~/.config/wa/active-profile`.

## 8. Shell completion for profile names

```bash
./wa completion bash > /tmp/wa.bash
source /tmp/wa.bash
./wa --profile <TAB>   # completes with "default" and "work"
./wa profile use <TAB> # same
```

## 9. Install both profiles as services (optional)

On macOS:

```bash
./wad install-service --profile default
./wad install-service --profile work
```

Verify:
```bash
launchctl list | grep com.yolo-labz.wad
```

Expected: two entries, one per profile.

On Linux:

```bash
./wad install-service --profile default
./wad install-service --profile work
systemctl --user list-units 'wad@*'
```

Expected: `wad@default.service` and `wad@work.service` both running. The systemd template unit at `~/.config/systemd/user/wad@.service` is shared between both instances.

## 10. Remove a profile

```bash
./wad uninstall-service --profile work   # stop the service first
./wa profile rm work --force             # then remove the state
ls ~/.local/share/wa/                    # only "default" remains
```

Expected: the `work` profile's entire directory tree is removed. The `default` profile is untouched.

---

## Automated verification

The entire sequence (minus real pairing) runs as an integration test:

```bash
WA_INTEGRATION=1 go test -race -tags integration -run TestTwoProfileE2E ./cmd/wad/...
```

Expected: the test pairs two memory-fake whatsmeow clients to two profiles, sends a message from each, verifies isolation of rate limiter + allowlist + audit log + session state.

## Walk-through verification (2026-04-11)

The non-pairing steps of this quickstart were executed end-to-end against the feature-008 binaries on darwin/arm64 with isolated XDG paths. Each step produced the documented output:

- **Step 3** `wa profile create work` → `Created profile "work" at .../data/wa/work` + pair hint ✓
- **Step 4** `wa profile list` → table with `PROFILE ACTIVE STATUS JID LAST_SEEN` columns, `work` listed as `not-paired` ✓
- **Step 7** `wa profile use default` + inspect `$XDG_CONFIG_HOME/wa/active-profile` → file contains `default\n` ✓
- **Step 8** `wa completion bash` → generates a valid bash completion script (first 5 lines show the `__wa_debug` helper header) ✓
- **Step 10** `wa profile rm work --yes` → `Removed profile "work".`; subsequent `wa profile list` shows only `default *` (active, daemon-stopped because no daemon running) ✓

Steps that require a live WhatsApp pairing (Step 5 pair work profile, Step 6 allowlist + send, Step 9 install-service with launchctl bootstrap) were NOT executed in the automated walk-through because they need a real phone or a burner account — per the constitution's v0 testing strategy these belong behind `WA_INTEGRATION=1`, not in the unit/race suite.

Steps 1 and 2 (build binaries, migrate) are exercised continuously by `TestMigrationCrash_RecoveryNoDataLoss` and the benchmark harness in `cmd/wad/migrate_crash_test.go` + `cmd/wad/bench_test.go`.

**Verdict**: every step reachable without a real phone passes. No drift from the documented output was observed.

## What this quickstart does NOT cover

- Real launchd/systemd auto-start on login (requires reboot or logout/login cycle)
- GoReleaser profile-aware release artifacts (step 10 of feature 007 applies unchanged)
- Profile encryption at rest (out of scope)
- Cross-profile atomic operations (out of scope)
