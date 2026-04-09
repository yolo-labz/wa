# Research: Socket Primary Adapter

**Feature**: 004-socket-adapter
**Date**: 2026-04-08
**Status**: Complete; all decisions verified with primary sources

Every decision below is recorded as a D-block per the speckit convention, with Decision / Rationale / Alternatives / Source. Citations are primary sources (upstream docs, upstream source, maintainer blogs) per constitution principle V.

## Contradicts blueprint

One decision in this research **corrects** a path asserted in `CLAUDE.md` and must be surfaced here per constitution principle V.

- **CLAUDE.md** states the macOS socket fallback is `~/Library/Caches/wa/wa.sock` and implies `github.com/adrg/xdg`'s `RuntimeDir` provides that value on darwin.
- **Reality**: `adrg/xdg` issue #120 documents that on darwin, `xdg.RuntimeDir` resolves to `~/Library/Application Support`, not `~/Library/Caches/...`.
- **Resolution**: Keep CLAUDE.md's target path (`~/Library/Caches/wa/wa.sock`) and implement a manual darwin branch in `path_darwin.go` that computes it from `os.UserHomeDir()` directly. Continue using `adrg/xdg` for `DataHome`, `ConfigHome`, `StateHome`, and `CacheHome` on both platforms. Rationale for keeping `~/Library/Caches/` as the target: Apple's File System Programming Guide classifies `~/Library/Caches/` as non-critical, recomputable, excluded from Time Machine backups and iCloud sync by default — exactly right for a transient IPC socket. `~/Library/Application Support` is for persistent per-user state and is backed up, which is the wrong semantics for a socket.

This is a clarification of the blueprint, not a rejection. `CLAUDE.md` will not be amended; this research document is the authoritative source for the darwin path going forward.

Source: [adrg/xdg issue #120](https://github.com/adrg/xdg/issues/120); [Apple File System Programming Guide — Library directory](https://developer.apple.com/library/archive/documentation/FileManagement/Conceptual/FileSystemProgrammingGuide/FileSystemOverview/FileSystemOverview.html).

---

## D1 — JSON-RPC 2.0 codec

**Decision**: Use `github.com/creachadair/jrpc2` with `channel.Line` framing on the accepted `net.Conn`. Enable `AllowPush` on the server options so the `subscribe` method can emit server-initiated notifications.

**Rationale**: `jrpc2` is actively maintained (v1.x API stable, commits through 2025), explicitly supports custom transports via its `channel.Channel` abstraction, ships a built-in `channel.Line` framing that matches the newline-delimited JSON wire format in `CLAUDE.md`, and supports server-initiated notifications via `AllowPush` — a hard requirement for the `subscribe` method. The `handler.Map` type provides a pluggable method dispatcher that cleanly accommodates the `Dispatcher` interface this feature defines as its seam. Dependency footprint is small: `jrpc2` plus the maintainer's `mds` utility module plus stdlib. The library passes `go test -race` upstream.

**Alternatives considered**:
- **Hand-rolled `encoding/json` with `bufio.Scanner` framing** — would reinvent request/response ID correlation, batch handling, and error-code mapping for no gain. Rejected.
- **`github.com/sourcegraph/jsonrpc2`** — maintained, 419 public importers, but its server-push path is LSP-header-framed (Content-Length headers), not newline-delimited. Forcing newline framing would require writing a custom `jsonrpc2.ObjectStream` and losing half the library's value. Rejected.
- **`net/rpc/jsonrpc` (stdlib)** — uses JSON-RPC 1.0 framing (no `jsonrpc` version field, no notifications, no batch), which is a different protocol. Rejected.

**Source**: [creachadair/jrpc2](https://github.com/creachadair/jrpc2) · [jrpc2 channel package](https://pkg.go.dev/github.com/creachadair/jrpc2/channel) · [sourcegraph/jsonrpc2](https://github.com/sourcegraph/jsonrpc2)

---

## D2 — Peer credential check without CGO

**Decision**: Build-tag-split files — `peercred_linux.go` and `peercred_darwin.go`.

On Linux:
```go
var cred *unix.Ucred
rawConn, _ := uc.SyscallConn()
rawConn.Control(func(fd uintptr) {
    cred, err = unix.GetsockoptUcred(int(fd), unix.SOL_SOCKET, unix.SO_PEERCRED)
})
```
Returns `*unix.Ucred{Pid, Uid, Gid}`. We only use `Uid`.

On macOS:
```go
var uid, gid uint32
rawConn, _ := uc.SyscallConn()
rawConn.Control(func(fd uintptr) {
    uid, gid, err = unix.Getpeereid(int(fd))
})
```
`unix.Getpeereid` is the portable BSD/darwin idiom; `unix.GetsockoptXucred(fd, unix.SOL_LOCAL, unix.LOCAL_PEERCRED)` is the lower-level equivalent exposing the group list, which we don't need.

**Rationale**: This matches the pattern documented in Tailscale's dedicated `tailscale.com/peercred` module and in production Go services. `x/sys/unix` exposes both syscalls natively; no CGO is required, satisfying constitution principle IV. Obtaining the file descriptor via `(*net.UnixConn).SyscallConn().Control(…)` is the idiomatic and race-free way to reach into a stdlib connection — the fd remains valid for the duration of the Control callback, preventing the "fd closed under us" problem documented in `golang/go#27613`.

**Alternatives considered**:
- **CGO `getpeereid` wrapper** — forbidden by constitution principle IV.
- **Parsing `/proc/net/unix`** — Linux-only, racy, required root for some fields. Rejected.

**Source**: [tailscale/peercred](https://github.com/tailscale/peercred) · [Using SO_PEERCRED in Go — jbowen.dev (2019)](https://blog.jbowen.dev/2019/09/using-so_peercred-in-go/) · [golang/go#27613](https://github.com/golang/go/issues/27613) · [x/sys/unix godoc](https://pkg.go.dev/golang.org/x/sys/unix)

---

## D3 — XDG_RUNTIME_DIR with darwin fallback

**Decision**: Platform-specific path computation in build-tagged files. Do NOT use `xdg.RuntimeDir` on darwin.

```go
// path_linux.go
//go:build linux
func socketDir() (string, error) {
    return filepath.Join(xdg.RuntimeDir, "wa"), nil
}

// path_darwin.go
//go:build darwin
func socketDir() (string, error) {
    home, err := os.UserHomeDir()
    if err != nil { return "", err }
    return filepath.Join(home, "Library/Caches/wa"), nil
}
```

The resolved socket path is `filepath.Join(socketDir(), "wa.sock")`.

**Rationale**: See §Contradicts blueprint above. `adrg/xdg` maps darwin's `RuntimeDir` to `~/Library/Application Support`, which is backed up by Time Machine and iCloud by default — wrong semantics for an ephemeral IPC socket. `~/Library/Caches/` is the Apple-documented location for recomputable non-critical per-user state and is excluded from backups.

**Alternatives considered**:
- **Accept `adrg/xdg`'s darwin default** — would violate `CLAUDE.md` §FS layout and store the socket in the backed-up directory. Rejected.
- **Mutate `xdg.RuntimeDir` at init time** — unsafe mutation of a package global, fragile. Rejected.

**Source**: [adrg/xdg issue #120](https://github.com/adrg/xdg/issues/120) · [adrg/xdg README](https://github.com/adrg/xdg)

---

## D4 — Line-delimited framing with 1 MiB cap

**Decision**: Delegate framing to `jrpc2`'s `channel.Line`, which uses a `bufio.Reader`-based line reader. Enforce the 1 MiB cap at the transport edge by wrapping the `net.Conn` with an `io.LimitReader` per framed message OR by configuring a custom `channel.Framing` that pre-validates size. On oversize, emit a JSON-RPC error frame (`-32004 OversizedMessage`, see D9) **and close the connection** — do not attempt to resync.

**Rationale**: `bufio.Scanner` has documented, undefined state after `bufio.ErrTooLong` (golang/go issues #65257 and #26431), so continuing on the same stream after an oversize risks desync. Closing is the idiomatic, safe response. The 1 MiB cap is well above plausible JSON-RPC request sizes (text messages bottom out at hundreds of bytes) and well below the 16 MiB media cap declared in feature 002's domain invariants — media should never traverse the socket as inline JSON; it goes through a sidecar path that is out of scope for this feature.

**Alternatives considered**:
- **`bufio.Reader.ReadLine`** — handles arbitrary length but loses the clean size cap. Rejected because oversized messages become a DoS vector.
- **`json.Decoder` directly on the conn** — no framing cap at all, unbounded allocation. Rejected.

**Source**: [bufio godoc](https://pkg.go.dev/bufio) · [golang/go#65257](https://github.com/golang/go/issues/65257) · [golang/go#26431](https://github.com/golang/go/issues/26431)

---

## D5 — Graceful shutdown pattern

**Decision**: Canonical Go pattern adapted from Eli Bendersky's graceful-shutdown writeup.

`Server` struct owns:
- `listener net.Listener` (created by `net.Listen("unix", path)`)
- `ctx context.Context` (with cancel, derived from caller's context)
- `wg sync.WaitGroup` (one per accepted connection)
- `shutdownDeadline time.Duration` (default 5s)

`Run(ctx)`:
1. Acquire the `lockedfile.Mutex` single-instance lock (D8)
2. `mkdir -p` the parent dir with mode 0700
3. `net.Listen("unix", path)`; `os.Chmod(path, 0600)` immediately
4. Accept loop in a goroutine; `wg.Add(1)` per accepted connection
5. Block on `ctx.Done()`
6. `listener.Close()` (causes pending `Accept` to return `net.ErrClosed`)
7. `waitWithDeadline(wg, shutdownDeadline)` — wait for in-flight connections to finish, with a hard deadline enforced by `context.WithTimeout`
8. For each active connection past the deadline: cancel the per-connection context, let the writer emit the final shutdown-in-progress error frame, close
9. `os.Remove(socketPath)`
10. Release the lockedfile mutex via its unlock function

`Shutdown()` is just `cancelFunc()`; `Wait()` blocks until step 10 completes.

**Rationale**: This is the de-facto idiom in the Go community and matches the shape used by `tsnet`, `signal-cli`'s daemon mode, and `cli/cli`'s daemon helpers. `signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)` is the modern stdlib entry point for the shutdown trigger and will be used by `cmd/wad/main.go` in feature 006.

**Alternatives considered**:
- **`errgroup.Group`** — equally valid but `sync.WaitGroup` is lower-ceremony here since we don't need aggregated errors from per-connection goroutines; errors are logged per-connection and shutdown is driven by context cancellation.
- **`defer listener.Close()` + `defer os.Remove()` in `main`** — fragile on panic paths and doesn't coordinate with in-flight drain. Rejected.

**Source**: [Eli Bendersky — Graceful shutdown of a TCP server in Go](https://eli.thegreenplace.net/2020/graceful-shutdown-of-a-tcp-server-in-go/) · [pkg.go.dev/os/signal#NotifyContext](https://pkg.go.dev/os/signal#NotifyContext)

---

## D6 — SO_PEERCRED race window

**Decision**: Accept the race; do NOT attempt pidfd mitigation in v0.

**Rationale**: The classic race — peer `exec`s between `accept()` and `getsockopt` — is well known. Linux 6.5 introduced `SO_PEERPIDFD` to address it for pid-based policies. For `wa`'s threat model the only thing we check is the peer **uid**, and uid does not change across `exec` within the same user session, so the race is benign for allowlisting. A separate CVE-class kernel race (`sock_getsockopt` lockless read of `sk_peer_cred`) was a data-leak fixed in stable kernels ≥ 5.4.151 / 5.10.71 / 5.14.10; any modern target has it. On darwin the same logic applies: uid is stable across the accept/getsockopt window. If pid-based logging or allowlisting is added later, upgrade to `SO_PEERPIDFD` on Linux ≥ 6.5 and note the minimum kernel version; that's out of scope for v0.

**Alternatives considered**:
- **`SO_PEERPIDFD` + `pidfd_send_signal(0)` liveness check** — Linux-only, requires kernel ≥ 6.5, adds complexity for no policy benefit. Rejected for v0.

**Source**: [LKML 2020 — SO_PEERCRED race discussion](https://lkml.kernel.org/lkml/20200318122144.bzeb647w7ytloetn@wittgenstein/T/)

---

## D7 — Goroutine leak detection in tests

**Decision**: Use `go.uber.org/goleak`. Wire `TestMain(m *testing.M) { goleak.VerifyTestMain(m) }` once per package (both `socket` and `sockettest`). In individual tests that spawn goroutines, additionally call `defer goleak.VerifyNone(t)` where per-test attribution is useful.

**Rationale**: goleak is the de-facto standard: actively maintained (last release December 2025), used by gRPC-go, etcd, and Uber internal services. No stdlib alternative exists; `runtime.NumGoroutine` diffing is the DIY path but misses stack-trace attribution, making failures hard to diagnose. goleak's `IgnoreCurrent` and `IgnoreTopFunction` options handle noisy background goroutines.

**Alternatives considered**:
- **`runtime.Stack` manual diff** — no attribution, noisy output. Rejected.
- **Custom counter in `testing.T.Cleanup`** — reinvents goleak badly. Rejected.

**Source**: [uber-go/goleak](https://github.com/uber-go/goleak) · [pkg.go.dev/go.uber.org/goleak](https://pkg.go.dev/go.uber.org/goleak)

---

## D8 — Reusing lockedfile for single-instance lock

**Decision**: Use `lockedfile.Mutex` on a sibling file `<socket>.lock`. The lock is held for the daemon's entire lifetime; the unlock function returned by `mu.Lock()` is deferred in `Server.Run` after successful listener setup.

```go
mu := &lockedfile.Mutex{Path: socketPath + ".lock"}
unlock, err := mu.Lock()
if err != nil {
    return nil, fmt.Errorf("another daemon holds the lock: %w", ErrAlreadyRunning)
}
defer unlock()
```

**Rationale**: `lockedfile.Mutex` is documented as "mutual exclusion within and across processes by locking a well-known file" — ideal for a daemon-lifetime lock. Feature 003's `sqlitestore/store.go` already uses `lockedfile.Edit` on a different file for the SQLite ratchet store; both `Edit` and `Mutex` sit atop the same flock primitive, so correctness is identical. `Mutex` more clearly signals "I don't read or write the file, I just hold the lock" — which is exactly the socket single-instance semantics.

Why a sibling `.lock` file rather than `flock(socketfd)`: `flock()` on a unix-socket inode is undefined on macOS (accepted but not documented to be respected), and portable behavior is only guaranteed on regular files. A sibling lock file is portable and matches feature 003's convention.

**Alternatives considered**:
- **Reuse `lockedfile.Edit`** for symmetry with feature 003 — acceptable but semantically misleading for a no-read/no-write lock. Rejected.
- **Raw `syscall.Flock`** — reinvents lockedfile's retry-on-EINTR and cross-platform logic. Rejected.

**Source**: [lockedfile godoc](https://pkg.go.dev/github.com/rogpeppe/go-internal/lockedfile) · [lockedfile/mutex.go source](https://github.com/rogpeppe/go-internal/blob/master/lockedfile/mutex.go) · `internal/adapters/secondary/sqlitestore/store.go` (feature 003 precedent)

---

## D9 — JSON-RPC 2.0 error code allocation

**Decision**: The following error code table. The block `-32011..-32099` is reserved for feature 005 and later; this feature must not use any code in that range.

| Code | Name | Origin | Who emits it |
|---|---|---|---|
| `-32700` | Parse error | JSON-RPC 2.0 spec | socket: malformed JSON or oversize |
| `-32600` | Invalid Request | JSON-RPC 2.0 spec | socket: missing `jsonrpc`/`method`, wrong id type |
| `-32601` | Method not found | JSON-RPC 2.0 spec | socket: unknown method name |
| `-32602` | Invalid params | JSON-RPC 2.0 spec | dispatcher: params fail validation |
| `-32603` | Internal error | JSON-RPC 2.0 spec | socket: recovered panic inside dispatcher |
| `-32000` | PeerCredRejected | server (this feature) | socket: peer uid mismatch at accept |
| `-32001` | Backpressure | server (this feature) | socket: outbound mailbox full, conn being closed |
| `-32002` | ShutdownInProgress | server (this feature) | socket: new request arrived during graceful shutdown |
| `-32003` | RequestTimeoutDuringShutdown | server (this feature) | socket: in-flight request cancelled at drain deadline |
| `-32004` | OversizedMessage | server (this feature) | socket: framed message exceeds 1 MiB |
| `-32005` | SubscriptionClosed | server (this feature) | socket: dispatcher's event source closed mid-stream |
| `-32006..-32010` | reserved for socket adapter future use | server (this feature) | — |
| `-32011..-32020` | **Reserved for feature 005 domain errors** (allowlist, rate-limit, not-paired, not-allowlisted, etc.) | server (feature 005) | dispatcher |
| `-32021..-32099` | Reserved for later features | server | — |

Every code in this table MUST appear in `contracts/wire-protocol.md` and the `errcodes.go` file. The table is append-only: existing codes MUST NOT be renumbered.

**Rationale**: The JSON-RPC 2.0 specification reserves `-32768..-32000` for "pre-defined errors"; `-32000..-32099` is the implementation-defined "Server error" range. Grouping transport failures at the low end (`-32000..-32010`) and leaving a contiguous block for feature 005's domain errors keeps the wire contract stable and machine-readable.

**Alternatives considered**:
- **HTTP-style codes in `data` field** — loses typed dispatch on the client side; requires string matching. Rejected.
- **Single `-32000` with string discriminator** — same problem. Rejected.

**Source**: [JSON-RPC 2.0 Specification §5.1](https://www.jsonrpc.org/specification)

---

## D10 — Bounded per-connection outbound mailbox

**Decision**: Each accepted connection owns a `chan []byte` with capacity 1024. Notification dispatch uses a non-blocking send idiom:

```go
select {
case c.out <- frame:
    // queued
default:
    return ErrBackpressure
}
```

A dedicated writer goroutine ranges over `c.out` and writes frames to the underlying `net.Conn`. When the subscribe/event path sees `ErrBackpressure`, it writes one final JSON-RPC error frame (code `-32001 Backpressure`) and closes the connection. Request/response frames travel a separate, blocking path — only unsolicited server-to-client notifications are subject to the drop-on-full policy.

**Rationale**: This is the canonical Go non-blocking channel idiom per Go-by-Example. Tailscale's DERP server uses exactly this shape for its per-client send queues so that a slow client cannot stall the hub. Capacity 1024 is a starting point; if telemetry in production shows significant backpressure closures, it can be raised in a later feature without a spec change.

**Alternatives considered**:
- **Unbounded slice queue** — unbounded memory under a slow reader. Rejected.
- **Per-message timeout on the writer** — adds latency, complicates shutdown. Rejected.
- **Block the producer (the dispatcher)** — head-of-line blocking across subscribers; one slow reader stalls the entire event stream. Rejected.

**Source**: [Go by Example — Non-Blocking Channel Operations](https://gobyexample.com/non-blocking-channel-operations) · [tailscale.com/derp](https://pkg.go.dev/tailscale.com/derp)

---

## D11 — Structured logging with log/slog

**Decision**: On each `accept()`, assign a monotonic `connID uint64` (atomic) and build a per-connection logger:

```go
connLog := s.log.With(
    slog.Uint64("conn", connID),
    slog.Uint64("peer_uid", uint64(uid)),
)
```

Store `*slog.Logger` on the `Connection` struct. Never call `slog.SetDefault` per-connection. Never log `params` or `result` content — only method names, connection ids, request ids, error codes, and latency numbers. This is enforced by code review and by the constitution's safety principle; no log-scrubbing middleware is needed because the socket adapter never sees message bodies as structured Go values — they pass through as opaque `json.RawMessage`.

**Rationale**: `slog.Logger.With` returns a new `Logger` backed by a cloned handler; safe for concurrent use by multiple goroutines; no mutation of the parent. Calling `With` once per connection amortizes the allocation cost. The `slog` godoc explicitly states that handlers must return a new handler from `WithAttrs`, so any third-party handler we might later swap in is required to honor the immutability contract.

**Alternatives considered**:
- **Stuffing the logger into `context.Context`** — acceptable but couples logging to the request-handling signature; direct field on the connection struct is clearer.
- **`slog.SetDefault` per connection** — mutates a global, races, breaks tests. Rejected.

**Source**: [pkg.go.dev/log/slog](https://pkg.go.dev/log/slog) — see `Logger.With` and `Handler.WithAttrs` contracts

---

## D12 — testing/synctest for deadline-sensitive tests

**Decision**: Use `testing/synctest` (Go 1.25 GA) for every test that asserts on a time deadline: graceful shutdown drain window, backpressure close timing, request cancellation at deadline, peer-cred hard-timeout. Put the server, a fake client connection, and the shutdown goroutine inside one bubble; call `synctest.Wait()` to quiesce; assert on fake-clock elapsed time. Zero `time.Sleep` in these tests.

**Rationale**: Inside a `synctest.Test` bubble, `time.Now`, `time.Sleep`, and `context.WithTimeout` use a fake clock that only advances when all goroutines are "durably blocked," making deadline tests deterministic and instant. This is the canonical use case in the upstream docs and is exactly what feature 003 used for its sqlitehistory tests. No injection of a clock abstraction is needed.

**Alternatives considered**:
- **Injected `clock.Clock` abstraction** — adds production surface area for a test-only need. Rejected.
- **Real `time.Sleep`** — flaky in CI under load, slow. Rejected.

**Source**: [pkg.go.dev/testing/synctest](https://pkg.go.dev/testing/synctest) · [Go blog — Testing Time](https://go.dev/blog/testing-time) · [Go 1.25 synctest example_test.go](https://github.com/golang/go/blob/release-branch.go1.25/src/testing/synctest/example_test.go)

---

## D13 — Integration test gating

**Decision**: **No test in feature 004 needs `//go:build integration`.** All tests run against in-process fakes: the `FakeDispatcher` in `sockettest/`, a unix-socket pair created via `net.Listen("unix", tempPath)` or `unix.Socketpair`, and `testing/synctest` for any timing. No whatsmeow, no WhatsApp network, no burner phone.

OS-specific tests (peer-cred behavior) use `//go:build linux` or `//go:build darwin` build tags. Those are OS gates, not network-integration gates, and they run in CI on both platforms.

**Rationale**: Feature 004's scope is transport + lifecycle + peer-cred + backpressure. Every dependency at that layer is either stdlib or a fake the feature itself ships. Gating any of these tests behind an env var would only hide fast, deterministic tests from CI. The existing `//go:build integration` + `WA_INTEGRATION=1` convention stays reserved for tests that actually touch the WhatsApp network layer — that's feature 003's territory and remains unchanged.

**Alternatives considered**:
- **Gate peer-cred tests behind `integration`** — no, they're not network-integration tests; they're OS-specific syscall tests that CI can and should run.

**Source**: [CLAUDE.md §Build/test commands](/Users/notroot/Documents/Code/WhatsAppAutomation/CLAUDE.md) · feature 004 `spec.md` (explicit design of fake-dispatcher-first testing)

---

## Summary of dependencies to add

The following will be added to `go.mod` when implementation begins:

```
require (
    github.com/creachadair/jrpc2 v1.x.x
    go.uber.org/goleak v1.x.x   // test only
)
```

All other imports (`golang.org/x/sys/unix`, `github.com/rogpeppe/go-internal/lockedfile`, `github.com/adrg/xdg`, `log/slog`) are already in `go.sum` from features 001–003.

Pinning the exact `jrpc2` and `goleak` versions is deferred to the implementation PR, where `go get` will pick the latest stable and Renovate will keep them current.
