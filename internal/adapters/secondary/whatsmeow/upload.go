package whatsmeow

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"strings"

	waClient "go.mau.fi/whatsmeow"
	waE2E "go.mau.fi/whatsmeow/proto/waE2E"
	"google.golang.org/protobuf/proto"

	"github.com/yolo-labz/wa/v2/internal/domain"
)

// mediaResolver is the minimal content-addressed read port the SHA256-source
// branch needs: load a stored MediaObject (carrying its on-disk path) by hash.
// It is the Resolve half of app.MediaStore, declared locally so the SHA256
// branch can read store bytes without buildMediaMessage holding the whole
// store surface. A miss surfaces as an error wrapping os.ErrNotExist. Spec 198.
type mediaResolver interface {
	Resolve(ctx context.Context, sha [32]byte) (domain.MediaObject, error)
}

// buildMediaMessage resolves the payload from the message's single source
// (Path, Bytes, or SHA256 — spec 197 "media byte seam"), enforces the 16 MiB
// ceiling, uploads the plaintext via whatsmeow, and returns the appropriate
// *waE2E.Message variant (Image/Video/Audio/Document) chosen by MIME.
//
// Source selection (domain.MediaMessage.SourceValidate already guarantees
// exactly one is set by the time Send reaches here):
//   - Path:   read the file on the daemon's filesystem (original behaviour,
//     preserved byte-for-byte).
//   - Bytes:  use the caller-inlined plaintext directly (remote clients).
//   - SHA256: load from the daemon's media store via store (spec 198). When
//     store is nil the seam stays honest and returns domain.ErrMediaUnsupported
//     rather than silently dropping.
//
// Feature 018 T1-08 / R-01 closes the gap where commit 4 only built a
// protobuf stub; spec 198 wires the SHA256 store read. Tests:
//   - TestMediaTooLarge32200 (unit, payload >16 MiB returns
//     domain.ErrMessageTooLarge mapped to -32201 MediaTooLarge at the
//     socket boundary).
//   - TestSendMediaSHA256FromStore (unit, the SHA256 branch loads from a fake
//     store and uploads the bytes).
//   - TestSendMediaRoundTrip (integration, WA_INTEGRATION=1) in
//     adapter_integration_test.go.
func buildMediaMessage(ctx context.Context, client whatsmeowClient, store mediaResolver, m domain.MediaMessage) (*waE2E.Message, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	payload, err := resolveMediaPayload(ctx, store, m)
	if err != nil {
		return nil, err
	}

	// MIME-based branch discriminator. The adapter boundary is where the
	// domain's mime string turns into a whatsmeow MediaType constant.
	appInfo := mediaTypeForMime(m.Mime)

	resp, err := client.Upload(ctx, payload, appInfo)
	if err != nil {
		return nil, fmt.Errorf("media upload: %w", err)
	}

	return composeMediaProto(m, resp), nil
}

// resolveMediaPayload returns the plaintext bytes for m, dispatching on the
// single payload source. Each branch enforces the MaxMediaBytes ceiling and
// rejects an empty payload, mirroring the original path-branch guards.
func resolveMediaPayload(ctx context.Context, store mediaResolver, m domain.MediaMessage) ([]byte, error) {
	switch {
	case len(m.Bytes) > 0:
		// Inline-bytes source (remote clients). The domain already capped
		// len(Bytes) ≤ MaxMediaBytes in SourceValidate, but re-check at the
		// adapter so this branch is self-contained and matches the path
		// branch's belt-and-suspenders cap (upload.go pre-197).
		if int64(len(m.Bytes)) > domain.MaxMediaBytes {
			return nil, fmt.Errorf("media inline bytes %d > %d: %w",
				len(m.Bytes), domain.MaxMediaBytes, domain.ErrMessageTooLarge)
		}
		return m.Bytes, nil

	case m.SHA256 != "":
		// SHA256 source (spec 198): a remote client referencing an object it
		// already uploaded (POST /media/upload) by content hash. Read the
		// bytes off the daemon's content-addressed store and upload them like
		// any other payload. The store is keyed by the 64-hex sha256, never by
		// client-supplied path — no traversal is possible here.
		return readMediaFromStore(ctx, store, m)

	default:
		// Path source — original daemon-filesystem behaviour, unchanged.
		return readMediaFile(m)
	}
}

// readMediaFromStore loads the content-addressed payload for m.SHA256 from the
// resolver and reads it off disk, enforcing the same MaxMediaBytes + empty
// guards as the path branch. When the resolver is nil the spec-197 seam stays
// honest: the SHA256 source is structurally valid but unserviceable, so it
// returns domain.ErrMediaUnsupported rather than a silent drop.
func readMediaFromStore(ctx context.Context, store mediaResolver, m domain.MediaMessage) ([]byte, error) {
	if store == nil {
		return nil, fmt.Errorf("media sha256 source has no media store wired: %w",
			domain.ErrMediaUnsupported)
	}
	sha, err := hexToSHA256(m.SHA256)
	if err != nil {
		return nil, fmt.Errorf("media sha256 %q: %w", m.SHA256, err)
	}
	obj, err := store.Resolve(ctx, sha)
	if err != nil {
		return nil, fmt.Errorf("media sha256 resolve: %w", err)
	}
	// obj.Path comes from the store keyed by the 64-hex sha — it is never
	// derived from caller-controlled input, so these reads are G304-safe
	// (gosec does not flag the indirect data flow through Resolve).
	info, err := os.Stat(obj.Path)
	if err != nil {
		return nil, fmt.Errorf("media sha256 stat: %w", err)
	}
	if info.Size() > domain.MaxMediaBytes {
		return nil, fmt.Errorf("media sha256 %s size %d > %d: %w",
			m.SHA256, info.Size(), domain.MaxMediaBytes, domain.ErrMessageTooLarge)
	}
	if info.Size() == 0 {
		return nil, fmt.Errorf("media sha256 %s is empty: %w", m.SHA256, domain.ErrEmptyBody)
	}
	payload, err := os.ReadFile(obj.Path)
	if err != nil {
		return nil, fmt.Errorf("media sha256 read: %w", err)
	}
	if int64(len(payload)) > domain.MaxMediaBytes {
		return nil, fmt.Errorf("media sha256 %s size %d > %d: %w",
			m.SHA256, len(payload), domain.MaxMediaBytes, domain.ErrMessageTooLarge)
	}
	return payload, nil
}

// hexToSHA256 decodes a 64-char lowercase hex sha256 into [32]byte. The app
// layer already validates the wire string, but the adapter re-checks so this
// branch is self-contained (matches the path/bytes branches' belt-and-braces
// posture).
func hexToSHA256(s string) ([32]byte, error) {
	var out [32]byte
	if len(s) != 64 {
		return out, errors.New("sha256 must be 64 hex characters")
	}
	raw, err := hex.DecodeString(s)
	if err != nil || len(raw) != 32 {
		return out, errors.New("sha256 is not valid hex")
	}
	copy(out[:], raw)
	return out, nil
}

// readMediaFile reads the daemon-local file at m.Path, enforcing the empty
// and MaxMediaBytes guards. Extracted verbatim from the pre-197
// buildMediaMessage path branch so existing path-based sends behave
// byte-for-byte — it deliberately reads m.Path (not a bare string param) to
// preserve the original gosec G304 data-flow shape the linter accepted.
func readMediaFile(m domain.MediaMessage) ([]byte, error) {
	// MS6: verify the path before touching the network. os is the only
	// stdlib caller here; the domain package intentionally owns no os.
	info, err := os.Stat(m.Path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, fmt.Errorf("media path: %w", err)
		}
		return nil, fmt.Errorf("media stat: %w", err)
	}
	if info.Size() > domain.MaxMediaBytes {
		return nil, fmt.Errorf("media %s size %d > %d: %w",
			m.Path, info.Size(), domain.MaxMediaBytes, domain.ErrMessageTooLarge)
	}

	// Inclusive guard on empty file — whatsmeow would accept zero bytes
	// silently which is almost never what the caller meant.
	if info.Size() == 0 {
		return nil, fmt.Errorf("media %s is empty: %w", m.Path, domain.ErrEmptyBody)
	}

	// Read the full payload. A streaming reader (UploadReader) would cut
	// one allocation but the 16 MiB ceiling puts an absolute upper bound
	// on the slice, so the plaintext+ciphertext copies are bounded too.
	// m.Path is from the caller process; the CLI binary and daemon run
	// as the same euid, and Validate() upstream rejects empty paths.
	payload, err := os.ReadFile(m.Path)
	if err != nil {
		return nil, fmt.Errorf("media read: %w", err)
	}
	if int64(len(payload)) > domain.MaxMediaBytes {
		return nil, fmt.Errorf("media %s size %d > %d: %w",
			m.Path, len(payload), domain.MaxMediaBytes, domain.ErrMessageTooLarge)
	}
	return payload, nil
}

// mediaTypeForMime maps an RFC-6838 MIME token to the whatsmeow MediaType
// keyring constant. The four coarse WhatsApp categories (image/video/audio/
// document) absorb every long-tail MIME via the `document` fallback, which
// matches the behaviour of the official WA clients.
func mediaTypeForMime(m string) waClient.MediaType {
	base := m
	if i := strings.IndexByte(m, ';'); i > 0 {
		base = strings.TrimSpace(m[:i])
	}
	base = strings.ToLower(base)
	switch {
	case strings.HasPrefix(base, "image/"):
		return waClient.MediaImage
	case strings.HasPrefix(base, "video/"):
		return waClient.MediaVideo
	case strings.HasPrefix(base, "audio/"):
		return waClient.MediaAudio
	default:
		return waClient.MediaDocument
	}
}

// composeMediaProto builds the waE2E.Message with the typed sub-message
// that matches the MIME category. Fields mirror whatsmeow's Upload example
// block in upload.go §Upload. Only fields the WA protocol requires are
// populated; thumbnails and waveform hints are a v0.6 follow-up.
func composeMediaProto(m domain.MediaMessage, resp waClient.UploadResponse) *waE2E.Message {
	mime := m.Mime
	fileLen := resp.FileLength

	switch mediaTypeForMime(m.Mime) {
	case waClient.MediaImage:
		img := &waE2E.ImageMessage{
			Mimetype:      proto.String(mime),
			Caption:       proto.String(m.Caption),
			URL:           proto.String(resp.URL),
			DirectPath:    proto.String(resp.DirectPath),
			MediaKey:      resp.MediaKey,
			FileEncSHA256: resp.FileEncSHA256,
			FileSHA256:    resp.FileSHA256,
			FileLength:    &fileLen,
		}
		return &waE2E.Message{ImageMessage: img}
	case waClient.MediaVideo:
		vid := &waE2E.VideoMessage{
			Mimetype:      proto.String(mime),
			Caption:       proto.String(m.Caption),
			URL:           proto.String(resp.URL),
			DirectPath:    proto.String(resp.DirectPath),
			MediaKey:      resp.MediaKey,
			FileEncSHA256: resp.FileEncSHA256,
			FileSHA256:    resp.FileSHA256,
			FileLength:    &fileLen,
		}
		return &waE2E.Message{VideoMessage: vid}
	case waClient.MediaAudio:
		aud := &waE2E.AudioMessage{
			Mimetype:      proto.String(mime),
			URL:           proto.String(resp.URL),
			DirectPath:    proto.String(resp.DirectPath),
			MediaKey:      resp.MediaKey,
			FileEncSHA256: resp.FileEncSHA256,
			FileSHA256:    resp.FileSHA256,
			FileLength:    &fileLen,
		}
		return &waE2E.Message{AudioMessage: aud}
	default:
		doc := &waE2E.DocumentMessage{
			Mimetype:      proto.String(mime),
			Caption:       proto.String(m.Caption),
			Title:         proto.String(basename(m.Path)),
			FileName:      proto.String(basename(m.Path)),
			URL:           proto.String(resp.URL),
			DirectPath:    proto.String(resp.DirectPath),
			MediaKey:      resp.MediaKey,
			FileEncSHA256: resp.FileEncSHA256,
			FileSHA256:    resp.FileSHA256,
			FileLength:    &fileLen,
		}
		return &waE2E.Message{DocumentMessage: doc}
	}
}

func basename(p string) string {
	if i := strings.LastIndexAny(p, "/\\"); i >= 0 {
		return p[i+1:]
	}
	return p
}
