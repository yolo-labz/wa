package app

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"time"

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
	return marshalResult(struct {
		Object       mediaObjectView `json:"object"`
		Cached       bool            `json:"cached"`
		BytesFetched int64           `json:"bytesFetched"`
	}{viewMediaObject(rep.Object), rep.Cached, rep.BytesFetched})
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
