package domain

import (
	"encoding/hex"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
)

// ErrMediaRef indicates a MediaRef or MediaObject failed its domain
// invariants (zero sha256, empty mime, non-positive size, missing ext).
var ErrMediaRef = errors.New("domain: invalid MediaRef")

// MediaRef is the content-addressed handle for a media object. The sha256
// field is authoritative; path/filesystem placement is derived from it.
type MediaRef struct {
	SHA256 [32]byte
	Mime   string
	Size   int64
	Ext    string // sniffed from content, NOT trusted sender metadata
}

// IsZero reports whether the ref is the zero value.
func (r MediaRef) IsZero() bool {
	return r.SHA256 == [32]byte{} && r.Mime == "" && r.Size == 0 && r.Ext == ""
}

// HexSHA256 returns the lowercase hex encoding of the sha256.
func (r MediaRef) HexSHA256() string { return hex.EncodeToString(r.SHA256[:]) }

// Validate enforces non-zero sha256, non-empty mime, positive size within
// the FR-052 ceiling (50 MiB), non-empty normalised extension.
func (r MediaRef) Validate() error {
	if r.SHA256 == [32]byte{} {
		return fmt.Errorf("%w: sha256 is zero", ErrMediaRef)
	}
	if strings.TrimSpace(r.Mime) == "" {
		return fmt.Errorf("%w: mime is empty", ErrMediaRef)
	}
	if r.Size <= 0 {
		return fmt.Errorf("%w: size %d must be positive", ErrMediaRef, r.Size)
	}
	if r.Size > MaxInboundMediaBytes {
		return fmt.Errorf("%w: size %d exceeds %d", ErrMessageTooLarge, r.Size, MaxInboundMediaBytes)
	}
	if strings.TrimSpace(r.Ext) == "" {
		return fmt.Errorf("%w: ext is empty", ErrMediaRef)
	}
	if strings.ContainsAny(r.Ext, "/\\.") {
		return fmt.Errorf("%w: ext %q must be bare (no separators or dots)", ErrMediaRef, r.Ext)
	}
	return nil
}

// RelativePath returns the path under $XDG_CACHE_HOME/wa/media/ derived
// from the sha256 and ext: "<first-2-hex>/<remaining-hex>.<ext>".
//
// The absolute path is produced by the MediaStore adapter which owns the
// XDG root. Keeping the layout function in domain guarantees path
// determinism across adapters and tests.
func (r MediaRef) RelativePath() string {
	h := r.HexSHA256()
	return filepath.Join(h[:2], h[2:]+"."+r.Ext)
}

// MaxInboundMediaBytes is the FR-052 ceiling on fetched inbound media; oversize
// attachments are rejected with -32111 media_too_large rather than being
// decrypted to disk.
const MaxInboundMediaBytes = 50 * 1024 * 1024

// MediaObject is the stored form of a media attachment: MediaRef plus
// adapter-observed detail (actual on-disk path, detected-vs-advertised mime,
// voice-note transcription, timestamps).
type MediaObject struct {
	Ref             MediaRef
	Path            string // absolute, 0600 perms, atomic tmp+rename writes
	MimeAdvertised  string // as the sender claimed (may lie)
	MimeDetected    string // net/http.DetectContentType result (authoritative)
	DurationSeconds int64  // non-zero for audio/video only
	Transcript      string // non-empty only for voice notes that have been transcribed
	FetchedAt       int64  // unix seconds
	LastAccessAt    int64  // unix seconds; GC input
}

// Validate defers to the embedded MediaRef and adds the object-level checks
// (non-empty path, non-empty detected mime, non-negative timestamps).
func (o MediaObject) Validate() error {
	if err := o.Ref.Validate(); err != nil {
		return err
	}
	if strings.TrimSpace(o.Path) == "" {
		return fmt.Errorf("%w: path is empty", ErrMediaRef)
	}
	if strings.TrimSpace(o.MimeDetected) == "" {
		return fmt.Errorf("%w: detected mime is empty", ErrMediaRef)
	}
	if o.FetchedAt < 0 || o.LastAccessAt < 0 {
		return fmt.Errorf("%w: negative timestamp", ErrMediaRef)
	}
	return nil
}

// MimeMismatch reports whether the sender's advertised mime disagreed with
// the detected mime. The socket layer surfaces this discrepancy separately
// in event payloads so that downstream policy can treat the mismatch as
// untrusted input.
func (o MediaObject) MimeMismatch() bool {
	if o.MimeAdvertised == "" {
		return false
	}
	return !strings.EqualFold(o.MimeAdvertised, o.MimeDetected)
}
