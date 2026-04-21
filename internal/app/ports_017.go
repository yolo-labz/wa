package app

import (
	"context"
	"regexp"
	"time"

	"github.com/yolo-labz/wa/internal/domain"
)

// This file declares the secondary ports introduced by feature 017
// (agent-experience). They are kept in a dedicated file for reviewability;
// the composition root wires concrete adapters to these interfaces in the
// same way as the pre-017 ports.
//
// Ports are additive: existing ContactDirectory and HistoryStore signatures
// are preserved byte-for-byte and the 017 extensions are introduced via
// optional extension interfaces (ContactSearcher, ThreadReader, etc.) that
// use cases type-assert on. This keeps older adapters building without
// forced stub implementations while still giving feature-017 adapters a
// clean named surface to satisfy.

// ContactSearcher is the FR-004/005/006/007 extension surface on the
// contact directory. Adapters that mirror contacts locally (contacts.db
// under Tier 1) implement this in addition to ContactDirectory.
type ContactSearcher interface {
	Search(ctx context.Context, query string, limit int) ([]domain.Contact, error)
	Annotate(ctx context.Context, jid domain.JID, notes string, tags []string) error
	ListChanged(ctx context.Context, sinceSeq int64, limit int) ([]domain.Contact, error)
	Sync(ctx context.Context, mode SyncMode) error
	Upsert(ctx context.Context, c domain.Contact) error
}

// SyncMode selects between incremental and full contact resynchronisation.
type SyncMode uint8

// SyncMode values. Zero is invalid.
const (
	SyncDelta SyncMode = iota + 1
	SyncFull
)

// String returns the canonical lowercase name used on the wire.
func (m SyncMode) String() string {
	switch m {
	case SyncDelta:
		return "delta"
	case SyncFull:
		return "full"
	default:
		return "unknown"
	}
}

// IsValid reports whether m is one of the two declared modes.
func (m SyncMode) IsValid() bool { return m == SyncDelta || m == SyncFull }

// ThreadCursor is the opaque, signed continuation token returned by
// ThreadReader.GetThread. It is stringly-typed on the wire.
type ThreadCursor string

// ThreadPage is the FR-011 response shape for thread retrieval.
type ThreadPage struct {
	Messages   []domain.Message
	Receipts   []domain.MessageReceipt
	Next       ThreadCursor // empty when no further pages
	HasMore    bool
}

// ThreadReader is the FR-010..FR-013 extension surface on the history
// store. Implementations persist message receipts and surface quoted /
// edited / revoked variants as first-class domain types.
type ThreadReader interface {
	GetThread(ctx context.Context, chat domain.JID, cursor ThreadCursor, limit int) (ThreadPage, error)
	PutReceipt(ctx context.Context, r domain.MessageReceipt) error
}

// SearchQuery is the FR-020 query shape. Either Phrase is non-empty OR at
// least one of All/Any is non-empty; raw FTS5 MATCH syntax is rejected at
// the socket-layer validator before reaching this port.
type SearchQuery struct {
	Phrase string
	All    []string
	Any    []string
	Chat   domain.JID // zero == global search
	Since  int64
	Until  int64
}

// SearchHit is a ranked BM25 result.
type SearchHit struct {
	MessageID domain.MessageID
	Chat      domain.JID
	Sender    domain.JID
	Snippet   string // attacker-controllable untrusted text; consumer MUST wrap
	TS        time.Time
	Score     float64 // BM25; higher is more relevant
}

// MessageSearcher is the FR-020..FR-024 extension surface on the history
// store.
type MessageSearcher interface {
	SearchBM25(ctx context.Context, q SearchQuery, limit int) ([]SearchHit, error)
}

// IdempotencyStore is the FR-030..FR-033 port binding an IdempotencyKey
// value to its cached result payload for 24 h.
type IdempotencyStore interface {
	// Check looks up key. When present with matching fingerprint it returns
	// the cached result bytes and replayed=true. When present with a
	// MISMATCHED fingerprint it returns domain.ErrIdempotencyConflict. When
	// absent, it returns replayed=false and a nil result.
	Check(ctx context.Context, profile string, key domain.IdempotencyKey) (result []byte, replayed bool, err error)

	// Record stores the result for a just-completed request so that future
	// replays of the same key + fingerprint return it. ExpiresAt must equal
	// now + IdempotencyKeyTTL per the data-model invariant.
	Record(ctx context.Context, profile string, key domain.IdempotencyKey, result []byte, expiresAt time.Time) error

	// Sweep removes rows with expires_at <= before and returns the number
	// deleted. Safe to call concurrently with Check/Record.
	Sweep(ctx context.Context, before time.Time) (int, error)
}

// DraftStore is the FR-040..FR-045 port covering the human-review queue.
// Transitions use the domain.Draft state machine; adapters persist the
// resulting row unchanged.
type DraftStore interface {
	Put(ctx context.Context, d domain.Draft) error
	Get(ctx context.Context, profile, id string) (domain.Draft, error)
	List(ctx context.Context, profile string, state domain.DraftState, limit int) ([]domain.Draft, error)
	Approve(ctx context.Context, profile, id string, at time.Time, by domain.DraftDecider) (domain.Draft, error)
	Reject(ctx context.Context, profile, id string, at time.Time, by domain.DraftDecider, reason string) (domain.Draft, error)
	ExpireDue(ctx context.Context, profile string, at time.Time) (int, error)
}

// MediaStore is the FR-050..FR-053 port covering content-addressed media
// storage, lazy fetch, and GC.
type MediaStore interface {
	// Resolve returns the MediaObject for sha. Returns os.ErrNotExist
	// (wrapped) when absent — callers MUST fall back to Download to force
	// a lazy fetch (FR-050 forbids Resolve from touching the network).
	Resolve(ctx context.Context, sha [32]byte) (domain.MediaObject, error)

	// Download lazy-fetches the payload for messageId via the whatsmeow
	// adapter, verifies sha256, and persists it to the content-addressed
	// path. Idempotent per FR-051: repeated calls on a cached payload
	// return cached=true and bytesFetched=0. Concurrent-download
	// throttling (≤ 4 in-flight per daemon) is enforced above this port.
	Download(ctx context.Context, messageID domain.MessageID, transcribe bool) (DownloadReport, error)

	// Write persists payload under the content-addressed path derived from
	// ref. Writes are atomic (tmp + rename) with 0600 perms. Returns the
	// final MediaObject including on-disk path and fetched_at.
	Write(ctx context.Context, ref domain.MediaRef, payload []byte, advertisedMime string, duration int64) (domain.MediaObject, error)

	// GC removes files whose last_access_at predates cutoff. DryRun=true
	// performs no deletions and returns the candidate list.
	GC(ctx context.Context, cutoff time.Time, dryRun bool) (GCReport, error)
}

// DownloadReport summarises a MediaStore.Download invocation (FR-050a).
type DownloadReport struct {
	Object       domain.MediaObject
	Cached       bool  // true when the payload was already on disk
	BytesFetched int64 // 0 when Cached==true
}

// GCReport summarises a MediaStore.GC invocation.
type GCReport struct {
	Candidates int
	Deleted    int
	BytesFreed int64
	DryRun     bool
}

// Transcriber is the FR-054..FR-055 port for speech-to-text over voice
// notes. Implementations spawn external binaries (whisper.cpp, Darwin
// `hear`, cloud Groq) and MUST honour ctx cancellation.
type Transcriber interface {
	// Transcribe returns the decoded text for the audio payload at path.
	// lang is a BCP-47 tag or "" for auto-detect. Empty result is valid
	// (silent audio); nil error and empty string MUST be distinguishable
	// from a transcription refusal (which returns a typed error).
	Transcribe(ctx context.Context, path string, lang string) (text string, detectedLang string, err error)
}

// EventBuffer is the durable ring-buffer backing the event stream
// (FR-060..FR-064). It is per-profile and monotonic in seq.
type EventBuffer interface {
	Append(ctx context.Context, rec EventRecord) error
	Range(ctx context.Context, sinceSeq int64, limit int) ([]EventRecord, error)
	Stats(ctx context.Context) (BufferStats, error)
	TrimTo(ctx context.Context, seq int64) (int, error)
}

// EventRecord is the persisted form of a domain.Event. trusted_json is
// whatsmeow-verified; untrusted_json is attacker-controllable per the
// injection firewall.
type EventRecord struct {
	Seq           int64
	Kind          string
	TrustedJSON   []byte
	UntrustedJSON []byte
	CreatedAt     time.Time
}

// BufferStats is returned by EventBuffer.Stats for degraded-event
// emission (FR-063) and status reporting.
type BufferStats struct {
	Size          int
	Capacity      int
	OldestSeq     int64
	NewestSeq     int64
	DroppedTotal  int64
}

// EventBus is the in-process fan-out that feeds subscribe connections.
// Subscribe returns a filtered stream; Publish is called by the whatsmeow
// adapter for every translated event.
type EventBus interface {
	Subscribe(ctx context.Context, f SubscribeFilter) (EventSubscription, error)
	Publish(ctx context.Context, rec EventRecord) error
}

// SubscribeFilter is the DSL from data-model §SubscriptionCursor +
// FR-060. Filter evaluation is AND across keys, OR within lists. BodyRe
// is compiled once at subscribe.
type SubscribeFilter struct {
	Kinds       []string
	Chats       []domain.JID
	Senders     []domain.JID
	NotSenders  []domain.JID
	BodyRe      *regexp.Regexp
	Since       int64 // Kafka-style cursor ack for gapless resume
}

// ReplySender is the FR-070 extension surface on MessageSender for quoted
// replies. Adapters that wire SendRequestExtra.InReplyTo implement this in
// addition to MessageSender.
//
// Implementations MUST:
//   - Validate msg.Validate() before any I/O.
//   - Reject a zero quotedID with a typed error.
//   - Honour ctx cancellation.
//   - Return a non-zero domain.MessageID on success.
type ReplySender interface {
	SendReply(ctx context.Context, quotedID domain.MessageID, msg domain.Message) (domain.MessageID, error)
}

// PresenceSender is the FR-071 port for typing/recording indicators. Rate
// limited to 1 send/chat/second at the adapter boundary; excess calls
// silently drop (no error) per the contract.
type PresenceSender interface {
	// SendComposing emits a chat-presence update. state is "composing",
	// "recording", or "paused". duration is advisory and MAY be ignored.
	// Calls that would exceed the 1/s/chat budget return nil without
	// contacting the network.
	SendComposing(ctx context.Context, chat domain.JID, state string, duration time.Duration) error
}

// ScheduledStore is the FR-111 port for durable scheduled-send storage.
// Adapters persist every state transition so that a daemon restart can
// reload pending schedules within ±5 s of their fire_at (SC-08).
type ScheduledStore interface {
	// Put inserts a new pending ScheduledSend. Returns an error if the id
	// already exists under the same profile.
	Put(ctx context.Context, s domain.ScheduledSend) error

	// Update replaces an existing row. Adapters MUST refuse to regress a
	// terminal state back to pending (the domain already rejects this,
	// but the port contract pins the guarantee).
	Update(ctx context.Context, s domain.ScheduledSend) error

	// Get returns the schedule for (profile, id) or domain-wrapped
	// os.ErrNotExist when absent.
	Get(ctx context.Context, profile, id string) (domain.ScheduledSend, error)

	// ListPending returns every non-terminal row for profile ordered by
	// fire_at ASC. Used at daemon boot to reload timers.
	ListPending(ctx context.Context, profile string) ([]domain.ScheduledSend, error)

	// List returns every row matching (profile, state). Passing the zero
	// ScheduledState returns all states.
	List(ctx context.Context, profile string, state domain.ScheduledState, limit int) ([]domain.ScheduledSend, error)

	// Delete removes a schedule row. Used only for operator cleanup;
	// cancellation goes through Update with MarkCancelled.
	Delete(ctx context.Context, profile, id string) error
}

// Embedder is the FR-102 port for turning a message body into a dense
// vector. Adapters wrap llama.cpp, cloud embedders, or Darwin NLEmbedding.
//
// Implementations MUST:
//   - Return a vector whose len equals the Dim they advertise via Info.
//   - Honour ctx cancellation (network/exec adapters MUST kill the child).
//   - Normalise to unit length so consumers can use cosine = dot(x,y).
//   - Use the Model string returned by Info so produced Embeddings round-trip.
type Embedder interface {
	// Embed returns a single vector for text. The returned Embedding's
	// MessageID is set by the caller (use cases) — adapters may leave it
	// empty.
	Embed(ctx context.Context, text string) (domain.Embedding, error)

	// Info returns the adapter's identity: the opaque model tag and the
	// fixed output dimensionality. Both are stable for the adapter's
	// lifetime.
	Info() EmbedderInfo
}

// EmbedderInfo is the non-secret metadata every Embedder must expose. The
// Dim is load-bearing: the vector index schema keys on it, and mixing two
// embedders with different dims corrupts the space.
type EmbedderInfo struct {
	Model string
	Dim   int
}

// VectorIndex is the FR-101 port binding an Embedding to a cosine-KNN
// search surface. Adapters wrap sqlite-vec (default) or a pure-Go brute
// fallback for < 10 000 rows.
type VectorIndex interface {
	// Upsert stores or replaces the vector for e.MessageID. Dim must match
	// the index's declared dimensionality (set at open).
	Upsert(ctx context.Context, e domain.Embedding) error

	// Knn returns the top-k nearest neighbours to query under cosine
	// similarity, highest score first. Returned slice length is ≤ k.
	Knn(ctx context.Context, query []float32, k int) ([]VectorHit, error)

	// Purge drops every vector. Used by `wa embeddings purge`.
	Purge(ctx context.Context) error

	// Size returns the count of stored vectors (O(1) when cached).
	Size(ctx context.Context) (int, error)
}

// VectorHit is a ranked cosine-similarity result.
type VectorHit struct {
	MessageID domain.MessageID
	Score     float32 // cosine similarity in [-1, 1]; higher is nearer
}

// EventSubscription is the client-observed stream.
type EventSubscription interface {
	// Events returns a channel closed by the EventBus when the subscription
	// terminates. Payloads are already filter-matched.
	Events() <-chan EventRecord

	// Close releases the subscription. Idempotent.
	Close() error

	// Ack records that the client has durably committed everything up to
	// and including seq. The EventBus MAY compact below this watermark.
	Ack(seq int64) error
}

// LabelManager is the FR-120..122 port for WhatsApp Business label
// operations. Adapters implement this against the whatsmeow Business
// surface (BuildLabelEdit + appstate CollectionName.LabelEdit) or, in
// tests, an in-memory fake. Use cases guard every call with a
// Business-device + feature-flag check; personal-account sessions MUST
// return ErrLabelsUnsupported from the socket layer before this port is
// touched.
//
// Implementations MUST:
//   - Use the given profile for all writes so that multi-profile daemons
//     cannot leak labels across accounts.
//   - Treat label names as opaque user-visible UTF-8 strings.
//   - Honour ctx cancellation (network-backed adapters MUST return
//     ctx.Err() from in-flight ops).
//   - Round-trip in ≤ 30 s measured end-to-end for Create+Assign+Unassign
//     against a live device (FR-121).
//   - Be safe for concurrent use by multiple goroutines.
type LabelManager interface {
	// ListLabels returns every label known to the device under profile. The
	// returned slice is a snapshot; callers may mutate it freely.
	ListLabels(ctx context.Context, profile string) ([]domain.Label, error)

	// CreateLabel persists a new label with the given name + palette colour.
	// The adapter assigns an id; implementations MUST return the persisted
	// Label so the caller sees the id. Duplicate names are permitted per
	// WhatsApp Business semantics — the id is the identity.
	CreateLabel(ctx context.Context, profile, name string, colorIndex int) (domain.Label, error)

	// DeleteLabel removes a label by id. Idempotent: deleting an unknown id
	// returns nil. Cascading removal of assignments is an adapter concern.
	DeleteLabel(ctx context.Context, profile, labelID string) error

	// Assign attaches a label to a chat or message. Idempotent: re-assigning
	// an existing (label, target) pair returns nil.
	Assign(ctx context.Context, profile string, assignment domain.LabelAssignment) error

	// Unassign detaches a label from a chat or message. Idempotent: removing
	// an assignment that does not exist returns nil.
	Unassign(ctx context.Context, profile string, assignment domain.LabelAssignment) error

	// ListAssignments returns every assignment for the given labelID under
	// profile. Returned slice may be empty but is never nil.
	ListAssignments(ctx context.Context, profile, labelID string) ([]domain.LabelAssignment, error)
}
