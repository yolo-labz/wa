# Feature 110e — Research: design decisions

Five decision records (DR). Each names ≥ 1 rejected alternative with reason, per Constitution §IV.20 (Nygard ADR / MADR completeness).

---

## DR-001: SSH transport — `os/exec` ssh binary vs Go-native SSH client

**Decision**: Wrap the system `ssh` binary via `os/exec.Command("ssh", ...)`.

**Rationale**:

1. The operator's SSH agent (`ssh-agent`) and configuration (`~/.ssh/config`, `~/.ssh/known_hosts`, ProxyJump rules, `IdentityFile` per Host blocks) ARE the auth source. A Go-native SSH client would have to re-implement `~/.ssh/config` parsing and re-prompt for key passphrases — both are solved problems that the system `ssh` binary already handles perfectly.
2. Zero new dependencies. Plan constraint #2 forbids dep additions; `golang.org/x/crypto/ssh` would add ~300 KB to the binary and a transitive `golang.org/x/sys` bump.
3. PTY allocation is one flag away (`ssh -t`). The Go-native equivalent requires manual `os.Pipe` + `pty.Start` plumbing.
4. Operators already trust the system `ssh` binary. Replacing it with a Go-native client raises a fresh attack surface (key handling, host-key verification semantics).

**Alternatives considered**:

- **A. `golang.org/x/crypto/ssh` Go-native client.** Rejected: forces re-implementation of `~/.ssh/config`, agent socket protocol, ProxyJump chains, multiplexing. None are required for the pair UX.
- **B. Embedded `libssh2` via cgo.** Rejected: violates the `CGO_ENABLED=0` repo invariant (CLAUDE.md "Decisions already locked in" row "SQLite driver").
- **C. WebSocket-over-HTTPS pair-stream RPC.** See DR-003 — separately rejected at the architecture layer.

---

## DR-002: Flag value shape — `<host>:<app>` colon-separated string vs `--host` + `--app` pair vs `dokku+ssh://` URL scheme

**Decision**: Single string flag `--remote <host>:<app>`. Parser splits on first `:`.

**Rationale**:

1. Operators type the flag once and the values stay together (`ProxMox.Dokku:wa-burocracy` is one tab-completion target if the operator wraps `wa pair --remote` in a shell alias). Two flags double the typing.
2. The `:` separator mirrors common operator-grade conventions (`scp host:path`, `rsync host:path`, `docker host:container`). Operators recognise it.
3. URL-scheme paths (`dokku+ssh://ProxMox.Dokku/wa-burocracy`) are heavy for a binary CLI value: scheme + authority + path = three things to type for two pieces of data. Net negative ergonomics.
4. Parser is trivial (`strings.Cut(s, ":")` + 4 invariant checks). The complexity budget for a single-feature CLI flag should not include a URL parser.

**Alternatives considered**:

- **A. Two separate flags `--host ProxMox.Dokku --app wa-burocracy`.** Rejected: doubles operator typing; loses the visual unity that hints "these are paired".
- **B. URL scheme `--remote dokku+ssh://ProxMox.Dokku/wa-burocracy`.** Rejected: spec §"Alternatives rejected" §C — flag-overload trap, and the colon form is already idiomatic in the operator domain.
- **C. Implicit from env (`WA_REMOTE_HOST` + `WA_REMOTE_APP`).** Rejected for the pair path because pair is rare-and-deliberate; making it env-driven encourages cron'ing pair (anti-pattern). 110c's REST CLI mode uses env appropriately for steady-state commands; pair is the wrong shape for that.

---

## DR-003: Daemon untouched vs daemon-side `pair.stream` RPC

**Decision**: Zero daemon-side change. Pair flow inside the container is identical to today (`wa pair` invoked via `dokku enter`).

**Rationale**:

1. **Bootstrap recursion**: a freshly-deployed daemon has an empty `tokens.db`. If pair required a REST token, operator would need to pair to get a token — chicken/egg.
2. **Test surface**: daemon-side streaming RPC means three new failure modes (SSE/WebSocket disconnect, response back-pressure, idle timeout) for a feature exercised twice a year per operator.
3. **Trust model**: SSH key access to the dokku host IS the operator's trust grant. Reusing it for pair beats inventing a parallel token-bootstrap.
4. **Daemon stability**: FR-007 makes "daemon diff empty" a hard constraint. Zero daemon change ⇒ zero regression risk to any other daemon function.

**Alternatives considered**:

- **A. New `pair.stream` JSON-RPC method that pushes QR frames over SSE.** Rejected: bootstrap recursion + test surface + tokens-empty case.
- **B. Daemon-side `pair-link-code` REST endpoint that returns a single phone-code without QR.** Partially valid but already covered by existing `wa pair --phone` flag through the SSH chain. No daemon change needed.
- **C. Add a daemon `pair.ready` event that pushes via the SSE subscribe stream.** Rejected: still requires an existing token, so does not help fresh-deploy pair.

---

## DR-004: PTY allocation — `ssh -t` flag vs `pty` package vs no-PTY

**Decision**: `ssh -t` on the exec invocation. No PTY allocation in Go.

**Rationale**:

1. `ssh -t` forces TTY allocation on the remote side, which is exactly what `mdp/qrterminal/v3` needs to render half-blocks. Local terminal is the operator's existing TTY — already a real PTY.
2. `stdin`/`stdout`/`stderr` inherit from the parent process (we use `cmd.Stdin = os.Stdin` etc.). No `pty` package needed; the operator's terminal sees the QR directly.
3. `creack/pty` (the common Go PTY library) would force a cgo-free workaround for a need that has none — we are not multiplexing TTYs.
4. Operators familiar with `ssh -t` understand the failure modes (warning about no PTY, fallback to dumb terminal). Adding a Go-side PTY introduces unfamiliar failure shapes.

**Alternatives considered**:

- **A. Allocate a Go-side PTY with `github.com/creack/pty`.** Rejected: extra dependency, no functional benefit for inherited stdio.
- **B. No `-t` (pass-through stdio without forcing PTY).** Rejected: `wa pair` inside the container sees no TTY → qrterminal falls back to ASCII art that often does not render correctly over SSH non-tty channels. Reliable QR requires `-t`.
- **C. `-tt` (force PTY even when stdin is not a TTY).** Considered for the unusual case of a CI invocation. Rejected because pair-from-CI is OUT OF SCOPE (spec §Assumptions #5).

---

## DR-005: Argv escaping — `exec.Command(name, args...)` vs `exec.Command("sh", "-c", "...")`

**Decision**: `exec.Command("ssh", "-t", host, "dokku", "enter", app, "--", "/usr/local/bin/wa", "pair", extraFlags...)` — directly passing args, no shell.

**Rationale**:

1. **Shell-metacharacter safety**: operators pass `--phone +5511999999999` where the `+` triggers some shell expansions. `exec.Command` with explicit args does not invoke any shell — `+` arrives at the SSH-remote process untouched.
2. **Argv shape is testable**: a stub `ssh` binary in `$PATH` can print `$@` and compare to a fixture string. With a shell wrapper, the same test has to defeat quote stripping at multiple layers.
3. **Idempotency-key with special characters**: spec FR-005 requires `--idempotency-key` pass-through. ULIDs are safe, but hand-crafted operator keys may contain `$`, `\`, backticks. A shell wrapper would mangle these.
4. The `dokku enter <app> --` form ends Dokku's flag parsing and treats the remainder as the in-container command; the `wa` binary then sees `pair` plus its own flags. No double-parsing surprises.

**Alternatives considered**:

- **A. Build a single shell command string and pass to `/bin/sh -c`.** Rejected: every operator-supplied value would need single-quote escaping (`'foo'` → `'foo'`, but `foo's` → `'foo'\''s'`), and any escape miss is a remote command injection. Direct argv is safer by construction.
- **B. Use `os.StartProcess` for one fewer layer of abstraction.** Rejected: `os/exec` already provides what we need; `os.StartProcess` requires manual `os.ProcAttr` setup and forfeits `exec.LookPath` + context-aware cancellation if we ever want it.

---

## Cross-cutting findings

- The `mdp/qrterminal/v3` library used by daemon-side `wa pair` is SSH-PTY-safe (verified by reading the spec 001 research dossier reference + the README "half-block" mode). No client-side adjustment needed.
- The Dokku-app path `dokku enter <app>` enters the running web container as the deploy user (uid 65532 per `Dockerfile`). The container has `wa` at `/usr/local/bin/wa`. Verified live on ProxMox.Dokku at 23:25 local on 19/05/2026.
- The `wa pair` subcommand inside the container will refuse if device is already paired (current behaviour, untouched). Operators who want a fresh pair must wipe first via `wa panic` or `wa session logout-all`. Those are explicit OUT-OF-SCOPE for 110e — they get their own follow-up specs (110f / 110g) if needed.

## Open questions

None. All decisions resolved.
