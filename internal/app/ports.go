// Package app contains the application use cases and the seven port
// interfaces that bound the hexagonal core. Files under this package MUST
// NOT import "go.mau.fi/whatsmeow" or any of its subpackages — the
// .golangci.yml depguard rule "core-no-whatsmeow" enforces this
// mechanically.
package app

import (
	"context"

	"github.com/yolo-labz/wa/internal/domain"
)

// MessageSender is the secondary port for outbound message delivery.
//
// Implementations MUST:
//   - Validate msg.Validate() before any I/O; return the validation error
//     wrapped without contacting any external system. (contract MS2, MS3)
//   - Honour ctx cancellation and return ctx.Err() if it fires. (MS4)
//   - Return a non-zero domain.MessageID on success. (MS1)
//   - Be safe for concurrent use by multiple goroutines. (MS5)
//   - For MediaMessage with a missing Path, return an error wrapping a
//     filesystem "not found" indicator. (MS6)
type MessageSender interface {
	Send(ctx context.Context, msg domain.Message) (domain.MessageID, error)
	MarkRead(ctx context.Context, chat domain.JID, id domain.MessageID) error
}

// EventStream is the secondary port for inbound event delivery. It is
// pull-based by design: callers block in Next until an event is ready
// or ctx is done.
//
// Implementations MUST:
//   - Buffer at least one event between Next calls. (ES1, ES6)
//   - Honour ctx cancellation: a pending Next returns ctx.Err(). (ES2)
//   - Return events in EventID-monotonic order. (ES3)
//   - Never drop un-Ack'd events while the adapter is alive. (ES4)
//   - Return a typed error (never panic) from Ack on unknown ids. (ES5)
type EventStream interface {
	Next(ctx context.Context) (domain.Event, error)
	Ack(id domain.EventID) error
}

// ContactDirectory is the secondary port for contact metadata lookup.
//
// Implementations MUST:
//   - Return a typed "not found" error for unknown JIDs.
//   - Parse phone strings via domain.ParsePhone semantics in Resolve;
//     the whatsmeow adapter may additionally query the server but MUST
//     honour ctx cancellation.
type ContactDirectory interface {
	Lookup(ctx context.Context, jid domain.JID) (domain.Contact, error)
	Resolve(ctx context.Context, phone string) (domain.JID, error)
}

// GroupManager is the secondary port for group metadata lookup.
//
// Implementations MUST:
//   - Return Groups whose JID.IsGroup() is true, whose Participants are
//     user JIDs, and whose Admins ⊆ Participants.
//   - List() returns a snapshot slice; mutating it does not affect state.
//   - On empty store, List returns an empty (non-nil) slice and nil err.
type GroupManager interface {
	List(ctx context.Context) ([]domain.Group, error)
	Get(ctx context.Context, jid domain.JID) (domain.Group, error)
}

// SessionStore is the secondary port for session persistence. The
// Session value is the domain's opaque handle; the Signal Protocol
// material lives inside the implementation and never crosses this port.
//
// Implementations MUST:
//   - Load returns a zero Session (NOT an error) when no session exists.
//   - Save persists atomically; concurrent Saves are serialised.
//   - Clear is idempotent and returns nil on an already-empty store.
type SessionStore interface {
	Load(ctx context.Context) (domain.Session, error)
	Save(ctx context.Context, s domain.Session) error
	Clear(ctx context.Context) error
}

// Allowlist is the secondary port for the policy decision. It is the
// only port that does NOT take a context.Context: the decision is pure,
// in-memory, synchronous, and must not perform I/O. The single canonical
// implementation is *domain.Allowlist.
type Allowlist interface {
	Allows(jid domain.JID, action domain.Action) bool
}

// AuditLog is the secondary port for the append-only audit log. Every
// send, every authorization decision, every pairing attempt produces an
// entry. The constitution mandates this log is separate from the debug
// log and never auto-rotated.
//
// Implementations MUST:
//   - Persist before Record returns (no buffering).
//   - Be safe for concurrent use.
//   - Reject out-of-order writes with a typed error.
type AuditLog interface {
	Record(ctx context.Context, e domain.AuditEvent) error
}

// HistoryStore is the secondary port for historical message lookup. Added by
// feature 003 per CLAUDE.md §"Reliability principles" rule 20 (Cockburn:
// ports as intent of conversation, no fixed port count).
//
// Implementations MUST:
//   - Return at most `limit` messages older than `before` in the given chat,
//     ordered by timestamp descending.
//   - First read from local persistence (e.g. messages.db); if fewer than
//     `limit` messages are available locally and the underlying transport
//     supports on-demand backfill, fetch the remainder.
//   - Return an empty slice and nil error when no more messages exist (NOT
//     a typed error — empty is the success case for "you have everything").
//   - Honour ctx cancellation; long-running on-demand fetches MUST be
//     cancellable.
//   - Be safe for concurrent reads from multiple goroutines.
//
// Behavioural contract: see specs/003-whatsmeow-adapter/contracts/historystore.md
// (HS1–HS6 clauses).
type HistoryStore interface {
	LoadMore(ctx context.Context, chat domain.JID, before domain.MessageID, limit int) ([]domain.Message, error)
}
