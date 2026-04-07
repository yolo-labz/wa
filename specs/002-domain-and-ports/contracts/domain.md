# Contract: domain types

**Applies to**: `internal/domain/*.go`
**Enforced by**: spec FR-001..FR-006, FR-015; unit tests under `internal/domain/*_test.go`.

This file is the **construction, validation, and method contract** for every domain type. It is the literal source of `internal/domain/*.go` (modulo the `package` and `import` blocks). Any divergence is a documentation defect to fix in the same commit.

## Universal rules

1. **Zero non-stdlib imports.** Every file under `internal/domain/` may only import from the Go standard library. The `core-no-whatsmeow` `depguard` rule plus a manual review of `import` blocks during PR review enforces this. The list of stdlib packages this feature uses: `errors`, `fmt`, `strings`, `unicode`, `sync`, `time`. Nothing else.
2. **Zero context.Context.** Domain functions are pure and synchronous. The five port methods that take `ctx` live in `internal/app/ports.go`, never in `internal/domain`. Adding a `ctx` parameter to a domain function is a code-review violation.
3. **Errors wrap sentinels.** Every error returned from `internal/domain` MUST wrap one of the sentinels in `errors.go` via `fmt.Errorf("%w: %s", domain.ErrXxx, detail)` so callers can `errors.Is(err, domain.ErrXxx)`. Naked `errors.New` for non-sentinel errors is forbidden.
4. **No anemic types.** Every exported struct has at least one method beyond field accessors. The constitution's anti-pattern #2 is enforced at PR review.
5. **Validation in constructors.** Every type that has invariants exposes a `New<Type>` or `Parse<Type>` constructor that returns `(<Type>, error)`. The zero value of every type either is invalid (caught by `IsZero()`) or is meaningfully empty (e.g. an empty `[]JID`).
6. **Doc comments on every exported identifier.** `golangci-lint`'s `revive` rule `exported` enforces this. Doc comments name *what the value represents* and *what callers may assume*, not implementation detail.

## `errors.go`

Six sentinels, all `var X = errors.New(...)`:

| Sentinel | When returned |
|---|---|
| `ErrInvalidJID` | `Parse`, `ParsePhone`, `NewGroup` on malformed JID |
| `ErrInvalidPhone` | `ParsePhone` on phone outside [8,15] digits or with invalid characters after normalisation |
| `ErrEmptyBody` | `TextMessage.Validate` on empty body |
| `ErrMessageTooLarge` | `TextMessage.Validate` on body > 64KB; `MediaMessage.Validate` is delegated to the adapter and uses the same sentinel |
| `ErrUnknownAction` | `ParseAction` on unknown string |
| `ErrNotAllowed` | reserved for future use by `app` layer policy middleware; declared here so the constant is in one place |

## `ids.go`

Two named types over `string`. Each has:

```go
type MessageID string
func (id MessageID) String() string { return string(id) }
func (id MessageID) IsZero() bool   { return id == "" }
```

Identical shape for `EventID`. The named-type-not-alias choice means `var m MessageID = "x"` compiles but `var m MessageID = someString` does NOT compile without an explicit cast — preventing accidental cross-type assignment bugs.

## `action.go`

```go
type Action uint8

const (
    ActionRead Action = iota + 1  // zero is invalid; iota+1 makes the zero value catchable
    ActionSend
    ActionGroupAdd
    ActionGroupCreate
)
```

Methods: `String() string` (returns `"read"`, `"send"`, `"group.add"`, `"group.create"`), `ParseAction(s string) (Action, error)` (case-sensitive, returns `ErrUnknownAction` wrapped on miss), `IsValid() bool`.

**Test cases**: 5 — one per valid value, plus zero-is-invalid, plus parse-round-trip, plus parse-unknown.

## `jid.go`

```go
type JID struct {
    user   string  // unexported — callers cannot construct an invalid JID
    server string
}
```

Constants:

```go
const (
    serverUser  = "s.whatsapp.net"
    serverGroup = "g.us"
)
```

Constructors:

```go
func Parse(input string) (JID, error)
func ParsePhone(phone string) (JID, error)
func MustJID(input string) JID  // for tests; panics on error
```

`Parse` accepts:

- `"5511999999999"` → user JID (digits only, length in [8,15])
- `"+5511999999999"` → user JID (strips `+`)
- `"5511999999999@s.whatsapp.net"` → user JID (already in canonical form)
- `"120363042199654321@g.us"` → group JID
- Anything else → `ErrInvalidJID` wrapped with the input string

`ParsePhone` is strict: it MUST receive a phone-shaped input (digits, possibly with `+`, spaces, parentheses, hyphens). It normalises by stripping every non-digit and validates length.

Methods (all on value receiver, no pointer): `String()`, `User()`, `Server()`, `IsUser()`, `IsGroup()`, `IsZero()`. The `String()` round-trip is `Parse(j.String()) == j` for every `j` constructed by `Parse` or `ParsePhone`.

**Test cases** (table tests in `jid_test.go`): 25+ rows including:

- Empty string → `ErrInvalidJID`
- `"+55 (11) 99999-9999"` → `5511999999999@s.whatsapp.net`
- `"5511999999999@s.whatsapp.net"` → round-trip
- `"5511999999999@invalid.server"` → `ErrInvalidJID`
- `"abc@s.whatsapp.net"` → `ErrInvalidJID` (non-digit user)
- 7-digit number → `ErrInvalidPhone` (below E.164 minimum)
- 16-digit number → `ErrInvalidPhone` (above E.164 maximum)
- Group JID with non-digit characters in user → `ErrInvalidJID`
- Two `@` symbols → `ErrInvalidJID`
- `MustJID("invalid")` → panics
- Concurrent `Parse` from 8 goroutines → race-clean

## `contact.go`

```go
type Contact struct {
    JID      JID
    PushName string
    Verified bool
}

func NewContact(jid JID, pushName string) (Contact, error)
func (c Contact) IsZero() bool
func (c Contact) DisplayName() string
```

`NewContact` rejects a zero JID. `DisplayName()` returns `PushName` if non-empty, else `c.JID.User()` (so display never falls back to a raw "user" string for a JID with no push name — it uses the digits).

**Test cases**: 4 — happy, zero JID, empty push name, both non-empty.

## `group.go`

```go
type Group struct {
    JID          JID
    Subject      string
    Participants []JID
    Admins       []JID
}

func NewGroup(jid JID, subject string, participants []JID) (Group, error)
func (g Group) HasParticipant(j JID) bool
func (g Group) IsAdmin(j JID) bool
func (g Group) Size() int
```

Validation in `NewGroup`:

1. `jid.IsGroup()` else `ErrInvalidJID`
2. `len(subject) <= 100` else error wrapping the limit
3. every `participants[i].IsUser()` else `ErrInvalidJID`
4. `participants` is not nil and not empty (groups must have at least one member)

`Admins` is set later via a separate method (or not — feature 002 may defer admin tracking and add it in feature 003 when whatsmeow surfaces the data; record the deferral here if so).

**Test cases**: 6.

## `message.go`

The sealed-interface variant pattern from research.md D1:

```go
type Message interface {
    isMessage()
    To() JID
    Validate() error
}

const (
    MaxTextBytes  = 64 * 1024
    MaxMediaBytes = 16 * 1024 * 1024
)

type TextMessage struct {
    Recipient   JID
    Body        string
    LinkPreview bool
}

type MediaMessage struct {
    Recipient JID
    Path      string
    Mime      string
    Caption   string
}

type ReactionMessage struct {
    Recipient JID
    TargetID  MessageID
    Emoji     string
}
```

The unexported `isMessage()` method on each variant is the seal. Out-of-package types CANNOT satisfy `Message` because they cannot implement an unexported method.

**Validation contract per variant**:

- `TextMessage.Validate`: `Recipient.IsZero()` → `ErrInvalidJID`; `Body == ""` → `ErrEmptyBody`; `len(Body) > MaxTextBytes` → `ErrMessageTooLarge`.
- `MediaMessage.Validate`: `Recipient.IsZero()` → `ErrInvalidJID`; `Path == ""` → `ErrEmptyBody` (yes, reusing the sentinel; the message is "the body is missing"); `Mime == ""` → `ErrEmptyBody`. Size check is delegated to the adapter because the file is on disk and the domain has no filesystem access.
- `ReactionMessage.Validate`: `Recipient.IsZero()` → `ErrInvalidJID`; `TargetID.IsZero()` → `ErrEmptyBody`. Empty `Emoji` is ALLOWED — it means "remove the reaction".

**Test cases**: 12 — three variants × four (happy, zero recipient, empty body/path, oversized).

## `event.go`

Sealed interface `Event` with four variants: `MessageEvent`, `ReceiptEvent`, `ConnectionEvent`, `PairingEvent`. Each implements `isEvent()`, `EventID() EventID`, `Timestamp() time.Time`. The full struct fields are listed in `data-model.md`.

The supporting enums:

```go
type ReceiptStatus uint8
const (
    ReceiptDelivered ReceiptStatus = iota + 1
    ReceiptRead
    ReceiptPlayed
)

type ConnectionState uint8
const (
    ConnDisconnected ConnectionState = iota + 1
    ConnConnecting
    ConnConnected
)

type PairingState uint8
const (
    PairQRCode PairingState = iota + 1
    PairPhoneCode
    PairSuccess
    PairFailure
)
```

Each enum has `String()` and `IsValid()`. Zero value is invalid by construction.

**Test cases**: 8 — sum-type compilation test, four variant constructors, three enum round-trips.

## `session.go`

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
func (s Session) IsLoggedIn() bool
```

`NewSession` rejects a zero JID and `deviceID == 0`. `IsLoggedIn()` is `!s.IsZero() && s.deviceID > 0`.

**Test cases**: 4.

## `allowlist.go`

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
func (a *Allowlist) Entries() map[JID][]Action
func (a *Allowlist) Size() int
```

`Allows` is read-only and holds an `RLock`. `Grant`/`Revoke` hold a `Lock`. `Entries()` returns a defensive copy — mutating it does NOT affect the allowlist.

**Test cases**: 8 including:

- Empty allowlist → `Allows(any, any)` returns false
- `Grant(jid, ActionRead)` → `Allows(jid, ActionRead)` returns true
- `Grant(jid, ActionRead)` → `Allows(jid, ActionSend)` returns false
- `Grant(jid, ActionSend)` then `Revoke(jid, ActionSend)` → returns false
- `Grant(jid, ActionRead, ActionSend)` then `Revoke(jid, ActionRead)` → `Allows(jid, ActionSend)` still true
- `Grant` of unknown `Action` (the zero value) → no-op (defensive)
- 1000 parallel `Allows` reads from 8 goroutines → no race
- 100 parallel `Grant`/`Revoke` writes from 8 goroutines → no race, no panic

## `audit.go`

```go
type AuditAction uint8

const (
    AuditSend AuditAction = iota + 1
    AuditReceive
    AuditPair
    AuditGrant
    AuditRevoke
    AuditPanic
)

type AuditEvent struct {
    ID       EventID
    TS       time.Time
    Actor    string
    Action   AuditAction
    Subject  JID
    Decision string
    Detail   string
}

func NewAuditEvent(actor string, action AuditAction, subject JID, decision string, detail string) AuditEvent
func (e AuditEvent) String() string
```

`NewAuditEvent` does NOT take a timestamp — it stamps `time.Now()` internally. **This is the only domain function that touches `time.Now()` directly.** It is justified because the audit event MUST be stamped at construction time, and passing the clock as an argument every call site would invite drift.

`String()` returns a single-line JSON-ish format suitable for log output: `{"id":"...","ts":"2026-04-06T22:33:00Z","actor":"wad","action":"send","subject":"5511...@s.whatsapp.net","decision":"allow","detail":""}`. It is hand-rolled (no `encoding/json` import) to avoid pulling JSON into the domain and to keep escaping behaviour predictable.

**Test cases**: 4.

## Test counts (must hit before merging)

| File | Tests | Notes |
|---|---|---|
| `jid_test.go` | ~25 | table-driven |
| `action_test.go` | ~5 | |
| `contact_test.go` | ~4 | |
| `group_test.go` | ~6 | |
| `message_test.go` | ~12 | three variants × four cases |
| `event_test.go` | ~8 | sum-type compile + enum round-trips |
| `session_test.go` | ~4 | |
| `allowlist_test.go` | ~8 | including 2 race tests |
| `audit_test.go` | ~4 | |
| **Total** | **~76** | unit tests for the domain layer |

These are domain tests, not contract tests. The contract test suite under `internal/app/porttest/` adds another ~30 tests covering the seven port behaviours. Combined: ~106 test functions, all running in under 5 seconds (spec SC-002).
