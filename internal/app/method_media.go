package app

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"time"
	"unicode/utf8"

	"github.com/yolo-labz/wa/v2/internal/domain"
	"github.com/yolo-labz/wa/v2/internal/observability"
)

// MediaFetchBytesChunkBytes is the raw (pre-base64) byte ceiling for a single
// media.fetchBytes response. It is deliberately well under the socket
// transport's 1 MiB per-frame cap (internal/adapters/primary/socket/framecap.go
// maxFrameBytes): 512 KiB raw inflates to ~683 KiB once base64-encoded, leaving
// comfortable headroom for the JSON-RPC envelope. A remote client streaming a
// 50 MiB attachment loops media.fetchBytes (offset += len until eof) instead of
// pulling the whole object in one oversized frame. Do NOT raise this toward the
// frame cap — the chunk-plus-envelope-plus-base64 product must stay below it.
const MediaFetchBytesChunkBytes = 512 * 1024

// mediaFetchBytesSchema is the wire schema tag for media.fetchBytes responses.
// The method is named media.fetchBytes (camelCase, matching the other
// camelCase methods like chat.markUnread / contacts.profilePhoto), but the
// SCHEMA STRING is deliberately all-lowercase: the schemas_golden_test drift
// guard scans for `"wa.<lowercase-dotted>/vN"` literals, and the schema tag is
// an opaque wire identifier that need not character-match the method name.
const mediaFetchBytesSchema = "wa.media.fetchbytes/v1"

// mediaObjectView is the on-wire shape of a MediaObject.
//
// Transcript is the text of a (possibly attacker-authored) voice note —
// attacker-controllable. Per FR-005a it MUST NOT reach an LLM-facing
// response unwrapped, so viewMediaObject leaves Transcript empty and routes
// the value through the `<channel source="wa">` envelope in Channel instead.
// CLI renderers fall back to Channel when Transcript is empty.
type mediaObjectView struct {
	SHA256          string `json:"sha256"`
	Path            string `json:"path"`
	Mime            string `json:"mime"`
	MimeAdvertised  string `json:"mimeAdvertised,omitempty"`
	MimeMismatch    bool   `json:"mimeMismatch,omitempty"`
	Size            int64  `json:"size"`
	DurationSeconds int64  `json:"durationSeconds,omitempty"`
	Transcript      string `json:"transcript,omitempty"`
	FetchedAt       int64  `json:"fetchedAt,omitempty"`
	Channel         string `json:"channel,omitempty"`
}

func viewMediaObject(o domain.MediaObject) mediaObjectView {
	v := mediaObjectView{
		SHA256:          o.Ref.HexSHA256(),
		Path:            o.Path,
		Mime:            o.MimeDetected,
		MimeAdvertised:  o.MimeAdvertised,
		MimeMismatch:    o.MimeMismatch(),
		Size:            o.Ref.Size,
		DurationSeconds: o.DurationSeconds,
		FetchedAt:       o.FetchedAt,
	}
	// FR-005a: a transcript is the text of a (possibly attacker) voice note;
	// fold it into the channel envelope, leaving the raw field empty. A
	// MediaObject is content-addressed and carries no chat/sender JID, so both
	// are the zero JID (JID{}.String() == "") — escaping the transcript TEXT is
	// the security-critical part and holds regardless of the envelope attrs.
	v.Channel = ChannelWrapFieldsIf(o.Transcript, InboundFields{Body: o.Transcript}, domain.JID{}, domain.JID{}, 0)
	return v
}

// mediaResolveParams is the JSON-RPC params for "media.resolve".
type mediaResolveParams struct {
	SHA256 string `json:"sha256"`
}

// handleMediaResolve implements "media.resolve".
func (d *Dispatcher) handleMediaResolve(ctx context.Context, raw json.RawMessage) (json.RawMessage, error) {
	if d.media == nil {
		return nil, ErrMethodNotFound
	}
	ctx, span := observability.StartMedia(ctx, d.profile, "media.resolve")
	defer span.End()
	var p mediaResolveParams
	if err := parseParams(raw, &p); err != nil {
		return nil, err
	}
	sha, err := parseSHA256(p.SHA256)
	if err != nil {
		return nil, err
	}
	obj, err := d.media.Resolve(ctx, sha)
	if err != nil {
		return nil, fmt.Errorf("media.resolve: %w", err)
	}
	// Spec 110h FR-004: hydrate Transcript from sidecar when MediaStore
	// produced an empty value. Store lookup failures are logged and
	// swallowed — resolve is a read-only path, an unhydrated transcript
	// is preferable to a 5xx for an unrelated sqlite hiccup.
	if obj.Transcript == "" && d.transcripts != nil {
		if rec, gerr := d.transcripts.Get(ctx, obj.Ref.SHA256); gerr == nil && rec.Transcript != "" {
			obj.Transcript = rec.Transcript
		} else if gerr != nil && d.log != nil {
			d.log.Warn("media.resolve transcript hydrate failed", "err", gerr)
		}
	}
	return marshalResult(struct {
		Object mediaObjectView `json:"object"`
	}{viewMediaObject(obj)})
}

// mediaFetchBytesParams is the JSON-RPC params for "media.fetchBytes".
//
// offset is the absolute byte position to start reading at; length is the
// number of raw bytes the client wants. The handler clamps length to
// MediaFetchBytesChunkBytes so a single response never exceeds the socket
// frame cap regardless of what the client asks for — the client is expected
// to loop, advancing offset by the returned len(bytes) until eof is true.
type mediaFetchBytesParams struct {
	SHA256 string `json:"sha256"`
	Offset int64  `json:"offset"`
	Length int64  `json:"length"`
}

// mediaFetchBytesResult is the on-wire shape of a media.fetchBytes response.
// Bytes is a Go []byte, which encoding/json (un)marshals as base64 — the ×1.33
// inflation is bounded by clamping the read to MediaFetchBytesChunkBytes.
type mediaFetchBytesResult struct {
	Schema string `json:"schema"`
	SHA256 string `json:"sha256"`
	Size   int64  `json:"size"`
	Offset int64  `json:"offset"`
	Bytes  []byte `json:"bytes"`
	EOF    bool   `json:"eof"`
}

// handleMediaFetchBytes implements "media.fetchBytes" — the socket-transport
// counterpart to the REST GET /media/{sha256} byte stream (audit #4). A remote
// client reaching the daemon over an SSH-forwarded unix socket cannot os.Open
// the daemon-side path that media.resolve/media.download return, so it pulls the
// payload in frame-cap-sized chunks here instead.
//
// The MediaStore port exposes no read-by-sha primitive; like the REST handler
// (rest/server.go handleMediaFetch) this resolves the object to its on-disk
// path and reads the requested window directly. Reads are clamped to
// MediaFetchBytesChunkBytes so the base64-inflated response stays under the
// socket's 1 MiB frame cap, and to MaxInboundMediaBytes (50 MiB) so a single
// fetch cannot stream an object larger than the daemon would ever have cached.
func (d *Dispatcher) handleMediaFetchBytes(ctx context.Context, raw json.RawMessage) (json.RawMessage, error) {
	if d.media == nil {
		return nil, ErrMethodNotFound
	}
	ctx, span := observability.StartMedia(ctx, d.profile, "media.fetchBytes")
	defer span.End()
	var p mediaFetchBytesParams
	if err := parseParams(raw, &p); err != nil {
		return nil, err
	}
	sha, err := parseSHA256(p.SHA256)
	if err != nil {
		return nil, err
	}
	if p.Offset < 0 || p.Length < 0 {
		return nil, ErrInvalidParams
	}

	obj, err := d.media.Resolve(ctx, sha)
	if err != nil {
		return nil, fmt.Errorf("media.fetchBytes: %w", err)
	}
	size := obj.Ref.Size
	// Guard against an object the daemon would never have admitted: a cached
	// payload above the inbound ceiling indicates store corruption, not a
	// legitimate fetch target.
	if size > domain.MaxInboundMediaBytes {
		return nil, ErrMessageTooLarge
	}

	// An offset at or past EOF is a valid terminal poll: return an empty,
	// eof=true chunk so the client loop halts without erroring.
	if p.Offset >= size {
		return marshalResult(mediaFetchBytesResult{
			Schema: mediaFetchBytesSchema,
			SHA256: obj.Ref.HexSHA256(),
			Size:   size,
			Offset: p.Offset,
			Bytes:  []byte{},
			EOF:    true,
		})
	}

	// Clamp the requested length to the chunk ceiling and to what remains.
	want := p.Length
	if want == 0 || want > MediaFetchBytesChunkBytes {
		want = MediaFetchBytesChunkBytes
	}
	if remaining := size - p.Offset; want > remaining {
		want = remaining
	}

	chunk := make([]byte, want)
	n, err := d.readMediaAt(obj.Path, chunk, p.Offset)
	if err != nil {
		return nil, fmt.Errorf("media.fetchBytes: %w", err)
	}
	chunk = chunk[:n]

	return marshalResult(mediaFetchBytesResult{
		Schema: mediaFetchBytesSchema,
		SHA256: obj.Ref.HexSHA256(),
		Size:   size,
		Offset: p.Offset,
		Bytes:  chunk,
		EOF:    p.Offset+int64(n) >= size,
	})
}

// readMediaAt opens the daemon-local media file and reads len(buf) bytes
// starting at offset. The path originates from the MediaStore (keyed by a
// validated 64-hex sha), never from client argv, so it is G304-safe — mirrors
// the trust boundary of rest/server.go handleMediaFetch. io.ReadFull treats a
// short final chunk (the last window of the file) as success: io.EOF /
// io.ErrUnexpectedEOF after at least one byte is the expected tail, not a
// failure.
func (d *Dispatcher) readMediaAt(path string, buf []byte, offset int64) (int, error) {
	f, err := os.Open(path) //nolint:gosec // G304: path comes from the trusted MediaStore (64-hex sha key), not client input
	if err != nil {
		return 0, err
	}
	defer func() {
		if cerr := f.Close(); cerr != nil && d.log != nil {
			d.log.Warn("media.fetchBytes close failed", "err", cerr)
		}
	}()
	if _, err := f.Seek(offset, io.SeekStart); err != nil {
		return 0, err
	}
	n, err := io.ReadFull(f, buf)
	if err != nil && !errors.Is(err, io.EOF) && !errors.Is(err, io.ErrUnexpectedEOF) {
		return n, err
	}
	return n, nil
}

// mediaDownloadParams is the JSON-RPC params for "media.download".
type mediaDownloadParams struct {
	MessageID  string `json:"messageId"`
	Transcribe bool   `json:"transcribe,omitempty"`
}

// handleMediaDownload implements "media.download".
//
// Spec 110h FR-001..FR-005: when transcribe=true AND the object is audio
// (domain.MediaObject.IsAudio — which recognizes WhatsApp voice notes that
// sniff as the generic "application/ogg" container, issue #179), invoke the
// configured Transcriber adapter, persist the result via the TranscriptStore
// sidecar (content-addressed dedup), populate the wire Transcript field, and
// emit a synthetic `media.transcribed` event so async consumers see the new
// transcript without a follow-up poll.
//
// Failure modes (all surface as typed JSON-RPC errors, never silently no-op
// per CLAUDE.md rule 12):
//   - transcribe=true, audio/*, transcriber nil → -32115
//     transcriber_not_configured.
//   - transcribe=true, audio/*, adapter returns error → -32116
//     transcribe_failed (wrapped).
//
// Non-audio mime types and transcribe=false short-circuit before adapter
// invocation: the on-disk payload is returned verbatim.
func (d *Dispatcher) handleMediaDownload(ctx context.Context, raw json.RawMessage) (json.RawMessage, error) {
	if d.media == nil {
		return nil, ErrMethodNotFound
	}
	ctx, span := observability.StartMedia(ctx, d.profile, "media.download")
	defer span.End()
	var p mediaDownloadParams
	if err := parseParams(raw, &p); err != nil {
		return nil, err
	}
	if p.MessageID == "" {
		return nil, ErrInvalidParams
	}
	rep, err := d.media.Download(ctx, domain.MessageID(p.MessageID), p.Transcribe)
	if err != nil {
		return nil, fmt.Errorf("media.download: %w", err)
	}
	if p.Transcribe && rep.Object.IsAudio() {
		if err := d.transcribeAndHydrate(ctx, p.MessageID, &rep.Object); err != nil {
			return nil, err
		}
	}
	return marshalResult(struct {
		Object       mediaObjectView `json:"object"`
		Cached       bool            `json:"cached"`
		BytesFetched int64           `json:"bytesFetched"`
	}{viewMediaObject(rep.Object), rep.Cached, rep.BytesFetched})
}

// transcribeAndHydrate runs the spec-110h voice-note path: cache hit via
// TranscriptStore.Get → return cached; cache miss → run Transcriber, persist,
// emit synthetic event. Mutates *obj in place so the caller's response view
// carries the Transcript without a re-Resolve.
func (d *Dispatcher) transcribeAndHydrate(ctx context.Context, messageID string, obj *domain.MediaObject) error {
	// Content-addressed cache lookup — voice note forwarded across N chats
	// transcribes once and hydrates on every Download.
	if d.transcripts != nil {
		if rec, err := d.transcripts.Get(ctx, obj.Ref.SHA256); err == nil && rec.Transcript != "" {
			obj.Transcript = rec.Transcript
			return nil
		} else if err != nil && d.log != nil {
			d.log.Warn("media.download transcript cache get failed", "err", err)
		}
	}
	if d.transcriber == nil {
		return ErrTranscriberNotConfigured
	}
	text, detectedLang, err := d.transcriber.Transcribe(ctx, obj.Path, "")
	if err != nil {
		return fmt.Errorf("%w: %w", ErrTranscribeFailed, err)
	}
	obj.Transcript = text
	now := time.Now().UTC().Unix()
	if d.transcripts != nil && text != "" {
		rec := TranscriptRecord{
			SHA256:     obj.Ref.SHA256,
			Transcript: text,
			Lang:       detectedLang,
			Adapter:    d.transcriberName,
			CreatedAt:  now,
		}
		if err := d.transcripts.Upsert(ctx, rec); err != nil && d.log != nil {
			d.log.Warn("media.download transcript upsert failed", "err", err)
		}
	}
	if d.bridge != nil && text != "" {
		d.bridge.EmitSynthetic(domain.MediaTranscribedEvent{
			ID:        domain.EventID("transcribe-" + hex.EncodeToString(obj.Ref.SHA256[:8])),
			TS:        time.Unix(now, 0).UTC(),
			SHA256:    obj.Ref.SHA256,
			MessageID: domain.MessageID(messageID),
			Lang:      detectedLang,
			Chars:     utf8.RuneCountInString(text),
			Adapter:   d.transcriberName,
		})
	}
	return nil
}

// mediaGCParams is the JSON-RPC params for "media.gc".
type mediaGCParams struct {
	OlderThanSeconds int64 `json:"olderThanSeconds,omitempty"`
	DryRun           bool  `json:"dryRun,omitempty"`
}

// handleMediaGC implements "media.gc".
func (d *Dispatcher) handleMediaGC(ctx context.Context, raw json.RawMessage) (json.RawMessage, error) {
	if d.media == nil {
		return nil, ErrMethodNotFound
	}
	ctx, span := observability.StartMedia(ctx, d.profile, "media.gc")
	defer span.End()
	p := mediaGCParams{OlderThanSeconds: 30 * 86400}
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &p); err != nil {
			return nil, ErrInvalidParams
		}
	}
	if p.OlderThanSeconds <= 0 {
		return nil, ErrInvalidParams
	}
	cutoff := time.Now().Add(-time.Duration(p.OlderThanSeconds) * time.Second)
	rep, err := d.media.GC(ctx, cutoff, p.DryRun)
	if err != nil {
		return nil, fmt.Errorf("media.gc: %w", err)
	}
	return marshalResult(struct {
		Candidates int           `json:"candidates"`
		Deleted    int           `json:"deleted"`
		BytesFreed int64         `json:"bytesFreed"`
		DryRun     bool          `json:"dryRun"`
		Files      []GCCandidate `json:"files,omitempty"`
	}{rep.Candidates, rep.Deleted, rep.BytesFreed, rep.DryRun, rep.Files})
}

func parseSHA256(s string) ([32]byte, error) {
	var out [32]byte
	if len(s) != 64 {
		return out, ErrInvalidParams
	}
	raw, err := hex.DecodeString(s)
	if err != nil || len(raw) != 32 {
		return out, ErrInvalidParams
	}
	copy(out[:], raw)
	return out, nil
}
