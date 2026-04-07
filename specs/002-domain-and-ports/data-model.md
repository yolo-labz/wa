# Data Model: Domain Types and the Seven Port Interfaces

**Branch**: `002-domain-and-ports` · **Plan**: [`plan.md`](./plan.md) · **Research**: [`research.md`](./research.md)

This is the **Go-level** data model — the literal field types, methods, validation rules, and lifecycle states that `internal/domain/` will contain when feature 002 is implemented. Every type below has zero non-stdlib imports. The contract tests in [`contracts/ports.md`](./contracts/ports.md) and [`contracts/domain.md`](./contracts/domain.md) consume these types as-is; the implementation in `/speckit:implement` is a literal transcription.

## Package layout

```text
internal/domain/
├── errors.go      // sentinel errors
├── ids.go         // MessageID, EventID type aliases
├── action.go      // Action enum
├── jid.go         // JID value object + Parse + helpers
├── contact.go     // Contact struct
├── group.go       // Group struct
├── message.go     // Message sealed interface + variants
├── event.go       // Event sealed interface + variants
├── session.go     // Session opaque handle
├── allowlist.go   // Allowlist + decision method
└── audit.go       // AuditEvent struct
```

## Sentinel errors (`errors.go`)

```go
package domain

import "errors"

var (
    ErrInvalidJID       = errors.New("domain: invalid JID")
    ErrInvalidPhone     = errors.New("domain: invalid phone number")
    ErrEmptyBody        = errors.New("domain: message body must not be empty")
    ErrMessageTooLarge  = errors.New("domain: message exceeds size limit")
    ErrUnknownAction    = errors.New("domain: unknown action")
    ErrNotAllowed       = errors.New("domain: action not allowed for jid")
)
```

Every error returned from `internal/domain` MUST wrap one of these via `fmt.Errorf("%w: %s", ErrXxx, detail)` so callers can `errors.Is` for the category.

## ID types (`ids.go`)

| Type | Underlying | Notes |
|---|---|---|
| `MessageID` | `string` (named type, not alias) | Opaque to callers; whatsmeow's message IDs are base64ish; we never parse them |
| `EventID` | `string` (named type, not alias) | Internal sequence numbers; the daemon assigns them, the domain only stores them |

Both have a `String() string` method and an `IsZero() bool` method. Neither is comparable to a raw string without an explicit cast — this prevents accidental cross-type assignment bugs.

## `Action` enum (`action.go`)

```go
type Action uint8

const (
    ActionRead Action = iota + 1
    ActionSend
    ActionGroupAdd
    ActionGroupCreate
)

func (a Action) String() string
func ParseAction(s string) (Action, error)   // returns ErrUnknownAction wrapped
```

Validation rule: a zero `Action{}` value (numeric 0) is never valid; constructors and parsers reject it. The `iota + 1` start is deliberate so that the zero value is invalid by construction — a Go idiom for forcing explicit initialisation.

Exhaustivity rule: every switch on `Action` in this codebase MUST handle all four constants and end with `default: panic(...)` or `default: return ErrUnknownAction`. The `forbidigo` rule cannot enforce this; code review enforces it. When a fifth action is added, every switch site is found by `golangci-lint run --enable=exhaustive` (already enabled in `.golangci.yml`).

## `JID` value object (`jid.go`)

```go
type JID struct {
    user   string  // digits-only for users; group ID for groups
    server string  // "s.whatsapp.net" or "g.us"
}

func Parse(input string) (JID, error)         // accepts phone OR JID forms
func ParsePhone(phone string) (JID, error)    // strict E.164-style phone -> user JID
func MustJID(input string) JID                // panics on error; tests only

func (j JID) String() string                  // canonical "<user>@<server>"
func (j JID) User() string
func (j JID) Server() string
func (j JID) IsUser() bool                    // server == "s.whatsapp.net"
func (j JID) IsGroup() bool                   // server == "g.us"
func (j JID) IsZero() bool
```

**Validation rules**:

- **Phone shape**: input is normalised by stripping every byte that is not `0`–`9` (so `"+55 (11) 99999-9999"` becomes `"5511999999999"`). After normalisation, length MUST be in [8, 15] per ITU-T E.164. Outside that range → `fmt.Errorf("%w: %s", ErrInvalidPhone, input)`.
- **JID shape**: must contain exactly one `@`. The `server` part must be `s.whatsapp.net` or `g.us`. The `user` part must be non-empty and contain only `[0-9]` for `s.whatsapp.net` JIDs, or `[0-9-]` (with at least one digit) for `g.us` JIDs.
- **Zero value**: the zero `JID{}` is invalid. `IsZero()` reports it. `Parse("")` returns `ErrInvalidJID`. The struct fields are unexported so callers cannot construct an invalid `JID` from outside the package.

**Round-trip invariant**: `Parse(j.String()) == j` for every `j` constructed by `Parse` or `ParsePhone`. Verified by table tests in `jid_test.go`.

## `Contact` (`contact.go`)

```go
type Contact struct {
    JID      JID
    PushName string  // self-reported name from WhatsApp; untrusted
    Verified bool    // WhatsApp verified-business flag
}

func NewContact(jid JID, pushName string) (Contact, error)
func (c Contact) IsZero() bool
func (c Contact) DisplayName() string  // PushName if non-empty, else jid.User()
```

`PushName` is a non-validated string from the network; callers MUST NOT trust it for any policy decision. `Verified` is informational.

## `Group` (`group.go`)

```go
type Group struct {
    JID          JID
    Subject      string
    Participants []JID  // user JIDs only; never group JIDs
    Admins       []JID  // subset of Participants
}

func NewGroup(jid JID, subject string, participants []JID) (Group, error)
func (g Group) HasParticipant(j JID) bool
func (g Group) IsAdmin(j JID) bool
func (g Group) Size() int
```

**Validation rules**:

- `JID.IsGroup()` must be true; otherwise `ErrInvalidJID`.
- Every `Participants[i].IsUser()` must be true; otherwise `ErrInvalidJID`.
- `Admins` must be a subset of `Participants`.
- `Subject` length ≤ 100 bytes (WhatsApp limit; document the source in the `.go` file comment).

## `Message` sealed interface (`message.go`)

```go
type Message interface {
    isMessage()             // unexported sentinel — forbids out-of-package implementations
    To() JID                // recipient (user or group)
    Validate() error
}

const (
    MaxTextBytes  = 64 * 1024        // 64 KB
    MaxMediaBytes = 16 * 1024 * 1024 // 16 MB
)

type TextMessage struct {
    Recipient JID
    Body      string
    LinkPreview bool
}
func (TextMessage) isMessage()       {}
func (m TextMessage) To() JID         { return m.Recipient }
func (m TextMessage) Validate() error // ErrEmptyBody, ErrMessageTooLarge

type MediaMessage struct {
    Recipient JID
    Path      string  // local file path on the daemon's filesystem
    Mime      string
    Caption   string  // may be empty
}
func (MediaMessage) isMessage()       {}
func (m MediaMessage) To() JID         { return m.Recipient }
func (m MediaMessage) Validate() error // path non-empty, mime non-empty; size check happens in adapter

type ReactionMessage struct {
    Recipient JID
    TargetID  MessageID
    Emoji     string  // empty string removes the reaction
}
func (ReactionMessage) isMessage()    {}
func (m ReactionMessage) To() JID      { return m.Recipient }
func (m ReactionMessage) Validate() error
```

**Sum-type rules** (Decision D1 in research.md):

- Every variant implements the unexported `isMessage()` sentinel; out-of-package types CANNOT satisfy `Message`.
- Pattern-match in callers via `switch m := msg.(type) { case TextMessage: ...; case MediaMessage: ...; case ReactionMessage: ... }`. Missing branches are caught by `golangci-lint --enable=exhaustive`.
- `Validate()` is variant-specific; `TextMessage` rejects empty bodies and bodies > 64 KB; `MediaMessage` rejects empty paths and empty mimes; `ReactionMessage` rejects empty `TargetID` (empty `Emoji` is allowed and means "remove reaction").

## `Event` sealed interface (`event.go`)

```go
type Event interface {
    isEvent()
    EventID() EventID
    Timestamp() time.Time
}

type MessageEvent struct {
    ID        EventID
    TS        time.Time
    From      JID
    Message   Message
    PushName  string  // sender's display name; untrusted
}
func (MessageEvent) isEvent()              {}
func (e MessageEvent) EventID() EventID    { return e.ID }
func (e MessageEvent) Timestamp() time.Time { return e.TS }

type ReceiptEvent struct {
    ID        EventID
    TS        time.Time
    Chat      JID
    MessageID MessageID
    Status    ReceiptStatus  // delivered | read | played
}
// ... isEvent, EventID, Timestamp ...

type ConnectionEvent struct {
    ID    EventID
    TS    time.Time
    State ConnectionState  // disconnected | connecting | connected
}

type PairingEvent struct {
    ID    EventID
    TS    time.Time
    State PairingState  // qr_code | phone_code | success | failure
    Code  string         // QR raw text or phone-pair code
}
```

`ReceiptStatus`, `ConnectionState`, `PairingState` are `uint8` enums following the same `iota + 1` zero-is-invalid pattern as `Action`.

## `Session` opaque handle (`session.go`)

```go
type Session struct {
    jid       JID
    deviceID  uint16
    createdAt time.Time
}

func NewSession(jid JID, deviceID uint16, createdAt time.Time) (Session, error)
func (s Session) JID() JID
func (s Session) DeviceID() uint16
func (s Session) CreatedAt() time.Time
func (s Session) IsZero() bool
func (s Session) IsLoggedIn() bool  // jid is non-zero AND deviceID > 0
```

The actual Signal Protocol material — the prekeys, ratchets, registration ID — lives **inside the secondary adapter** (`internal/adapters/secondary/sqlitestore/`). The domain `Session` is intentionally opaque: it knows the JID and the device ID for routing, nothing else. This is the "library at arm's length" rule from Constitution Principle I in its purest form.

## `Allowlist` policy (`allowlist.go`)

```go
type Allowlist struct {
    mu      sync.RWMutex
    entries map[JID]actionSet
}

type actionSet struct {
    read        bool
    send        bool
    groupAdd    bool
    groupCreate bool
}

func NewAllowlist() *Allowlist
func (a *Allowlist) Allows(jid JID, action Action) bool
func (a *Allowlist) Grant(jid JID, actions ...Action)
func (a *Allowlist) Revoke(jid JID, actions ...Action)
func (a *Allowlist) Entries() map[JID][]Action  // copy; never the internal map
func (a *Allowlist) Size() int
```

**Default-deny rule**: an unknown JID returns `false` from `Allows`. This is the Constitution Principle III commitment encoded as a single line of behaviour.

**Concurrency rule**: `Allows` is read-only and holds an `RLock`. `Grant`/`Revoke` hold a `Lock`. The contract test suite includes a `t.Parallel()` race test.

**Tiered actions rule**: `Grant(jid, ActionRead)` does NOT imply `ActionSend`. Each action is tracked independently. `Grant(jid, ActionGroupAdd)` does NOT imply `ActionGroupCreate`. The constitution's "tiered actions" requirement is enforced by structure, not by convention.

**No SIGHUP, no file I/O, no clock**: this is a pure data structure. The future daemon (feature 004) wraps an `*Allowlist` with a file-watcher that calls `Grant`/`Revoke` on TOML edits; the watcher lives in the adapter, not in the domain.

## `AuditEvent` (`audit.go`)

```go
type AuditEvent struct {
    ID        EventID
    TS        time.Time
    Actor     string       // "wad" | "wa-cli" | "wa-assistant" | "human"
    Action    AuditAction  // separate enum from Action — these are AUDIT actions, not policy actions
    Subject   JID
    Decision  string       // "allow" | "deny" | "rate-limited" | "succeeded" | "failed"
    Detail    string       // free-form, may include error messages, never PII beyond JID
}

type AuditAction uint8

const (
    AuditSend AuditAction = iota + 1
    AuditReceive
    AuditPair
    AuditGrant
    AuditRevoke
    AuditPanic
)

func NewAuditEvent(actor string, action AuditAction, subject JID, decision string, detail string) AuditEvent
func (e AuditEvent) String() string  // single-line JSON for log output
```

`AuditEvent` is a value object; the `AuditLog` *port* (in `internal/app/ports.go`) is what writes them to disk. The audit log is append-only and never auto-rotated, per CLAUDE.md.

## State transitions

There are only two stateful entities in this feature, and both have minimal state machines.

### Session lifecycle

```text
Zero ──NewSession()──▶ LoggedIn ──Clear()──▶ Zero
                          │
                          └──ErrClientLoggedOut──▶ Zero
```

The `Session` value itself is immutable; "transitions" mean the `SessionStore` port replaces the value. The domain provides the snapshot, the adapter owns the transition mechanism.

### Allowlist lifecycle

```text
NewAllowlist() ──▶ empty
                     │
                     ├── Grant(jid, actions...) ──▶ entry added/updated
                     ├── Revoke(jid, actions...) ──▶ entry shrunk or removed
                     └── concurrent Allows() reads always see a consistent snapshot
```

The data structure is monotonic in the sense that `Allows` decisions are deterministic given the current snapshot; there are no scheduled transitions and no time-based decay. Time-based grants (`wa grant --ttl 5m`) are implemented in feature 004's adapter as a goroutine that schedules a `Revoke`, not as a TTL field on the entry.

## Invariants and where they live

| Invariant | Enforced by | File |
|---|---|---|
| JID syntax | `Parse`, `ParsePhone` | `jid.go` |
| Phone digit range 8–15 | `ParsePhone` | `jid.go` |
| Text body 0 < len ≤ 64 KB | `TextMessage.Validate` | `message.go` |
| Media path/mime non-empty | `MediaMessage.Validate` | `message.go` |
| Reaction has a target | `ReactionMessage.Validate` | `message.go` |
| Group JID is `g.us` | `NewGroup` | `group.go` |
| Group participants are user JIDs | `NewGroup` | `group.go` |
| Group admins ⊆ participants | `NewGroup` | `group.go` |
| Allowlist default deny | `Allowlist.Allows` | `allowlist.go` |
| Action zero is invalid | `Action` enum starts at `iota + 1` | `action.go` |
| Concurrent allowlist reads | `sync.RWMutex` | `allowlist.go` |

Every invariant is verifiable by a test in the corresponding `_test.go` file. The contract test suite in `internal/app/porttest/` does not duplicate these — it tests the *port behaviour*, not the domain construction. Domain unit tests live next to the domain code; port contract tests live under `porttest/`.

## What this data model is NOT

- It is NOT a wire format. JSON tags are absent because nothing in this feature serialises domain types over JSON-RPC; the future socket adapter (feature 004) defines its own DTOs and translates at the edge.
- It is NOT a database schema. SQLite columns live in `internal/adapters/secondary/sqlitestore/` (feature 003) and are translated to/from `Session` at the boundary.
- It is NOT a public API. The `internal/domain` package is `internal`, so out-of-tree consumers cannot import it. The `wa-assistant` plugin's MCP shim consumes the JSON-RPC wire format from feature 004, not these types.
- It is NOT exhaustive of every WhatsApp protocol message type. Stickers, polls, view-once, location, vCards, edits, deletes — all deferred. They get added via `Message` variants in later features and pass the existing contract suite by construction.
