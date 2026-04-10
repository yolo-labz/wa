# Quickstart: Binaries and Composition Root

**Feature**: 006-binaries-wiring
**Goal**: Build and run `wad` + `wa` from a fresh clone in under 5 minutes.

## 0. Prerequisites

```bash
go version        # 1.25+
git status        # On branch 006-binaries-wiring
```

## 1. Build both binaries

```bash
go build ./cmd/wad ./cmd/wa
```

Expected: two binaries `wad` and `wa` in the current directory. Zero CGO.

## 2. Run the daemon

```bash
./wad
```

Expected: structured JSON log on stderr showing startup: directories created, session store opened, socket listening. Socket at `~/Library/Caches/wa/wa.sock` (macOS) or `$XDG_RUNTIME_DIR/wa/wa.sock` (Linux).

## 3. Check status

```bash
./wa status
```

Expected: `{"connected": false}` (no device paired yet).

## 4. Pair (requires burner phone)

```bash
./wa pair
```

Expected: QR code in the terminal. Scan with WhatsApp. On success: `paired: true`.

## 5. Add a JID to the allowlist

```bash
./wa allow add 5511999999999@s.whatsapp.net --actions send
```

Expected: `added` on stdout. `~/.config/wa/allowlist.toml` now has a `[[rules]]` entry.

## 6. Send a message

```bash
./wa send --to 5511999999999@s.whatsapp.net --body "hello from wa"
```

Expected: `sent to 5511...@s.whatsapp.net at 15:04:05 (id: 3EB0...)` on stdout. Exit code 0.

## 7. Check audit log

```bash
cat ~/.local/state/wa/audit.log | head -5
```

Expected: JSON lines with `action: "send"`, `decision: "ok"`.

## 8. Graceful shutdown

Press `Ctrl-C` in the wad terminal. Expected: log shows "shutdown initiated", "socket unlinked", "session store closed". Process exits 0 within 2 seconds.

## 9. Run automated tests

```bash
go test -race ./cmd/wa/...         # testscript golden files
go test -race ./cmd/wad/...        # unit tests (non-integration)
go test -race ./...                # full repo
```

## 10. Run integration test (requires fake whatsmeow only)

```bash
WA_INTEGRATION=1 go test -race -tags integration ./cmd/wad/...
```

Expected: pair → allow → send cycle with fake client completes in <5s.

---

## What this quickstart does NOT cover

- GoReleaser cross-compilation — feature 007
- launchd/systemd unit files — feature 007
- Homebrew tap + Nix flake — feature 007
- Bash/zsh completions — feature 007
