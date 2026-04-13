# Data Model: Code Quality Audit & Modernization (016)

## No New Entities

This feature is a pure refactoring — no new domain entities, database tables, API endpoints, or data structures are introduced. All existing data models remain unchanged.

## Structural Changes (internal only)

The following existing structs are split into sub-structs for Single Responsibility Principle compliance. These are internal implementation details — no public API changes.

### whatsmeow.Adapter split

**Before**: Single `Adapter` struct with 13 fields.

**After**:
- `Adapter` — facade (≤7 fields), composes the sub-structs below
- `sessionManager` — manages session, history, allowlist references
- `auditWriter` — manages audit buffer and flush logic

### socket.Server split

**Before**: Single `Server` struct with 13+ fields.

**After**:
- `Server` — core listener + composition of sub-types
- `shutdownCoordinator` — shutdown flag, deadline, drain logic, notification dispatch
- `connRegistry` — connections map, mutex, add/remove/iterate methods

## Unchanged Entities

All domain types remain as-is:
- `domain.JID` (gains `slog.LogValuer` implementation — additive, not breaking)
- `domain.Message`, `domain.Contact`, `domain.Group`
- `domain.Allowlist`, `domain.Session`, `domain.Event`, `domain.AuditEvent`
- `domain.Action`, `domain.ReceiptStatus`, `domain.ConnectionState`

All port interfaces remain as-is:
- `app.MessageSender`, `app.EventStream`, `app.ContactDirectory`
- `app.GroupManager`, `app.SessionStore`, `app.Allowlist`
- `app.AuditLog`, `app.HistoryStore`
