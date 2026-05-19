package app

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/yolo-labz/wa/v2/internal/domain"
	"github.com/yolo-labz/wa/v2/internal/observability"
)

// mediaObjectView is the on-wire shape of a MediaObject.
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
}

func viewMediaObject(o domain.MediaObject) mediaObjectView {
	return mediaObjectView{
		SHA256:          o.Ref.HexSHA256(),
		Path:            o.Path,
		Mime:            o.MimeDetected,
		MimeAdvertised:  o.MimeAdvertised,
		MimeMismatch:    o.MimeMismatch(),
		Size:            o.Ref.Size,
		DurationSeconds: o.DurationSeconds,
		Transcript:      o.Transcript,
		FetchedAt:       o.FetchedAt,
	}
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

// mediaDownloadParams is the JSON-RPC params for "media.download".
type mediaDownloadParams struct {
	MessageID  string `json:"messageId"`
	Transcribe bool   `json:"transcribe,omitempty"`
}

// handleMediaDownload implements "media.download".
//
// Spec 110h FR-001..FR-005: when transcribe=true AND the detected mime is
// audio/*, invoke the configured Transcriber adapter, persist the result via
// the TranscriptStore sidecar (content-addressed dedup), populate the wire
// Transcript field, and emit a synthetic `media.transcribed` event so async
// consumers see the new transcript without a follow-up poll.
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
	if p.Transcribe && strings.HasPrefix(rep.Object.MimeDetected, "audio/") {
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
		Candidates int   `json:"candidates"`
		Deleted    int   `json:"deleted"`
		BytesFreed int64 `json:"bytesFreed"`
		DryRun     bool  `json:"dryRun"`
	}{rep.Candidates, rep.Deleted, rep.BytesFreed, rep.DryRun})
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
