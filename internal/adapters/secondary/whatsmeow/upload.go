package whatsmeow

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"

	waClient "go.mau.fi/whatsmeow"
	waE2E "go.mau.fi/whatsmeow/proto/waE2E"
	"google.golang.org/protobuf/proto"

	"github.com/yolo-labz/wa/v2/internal/domain"
)

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
//   - SHA256: load from the daemon's media store. NOT YET WIRED — see TODO
//     below; returns a clear error so the seam is honest instead of silent.
//
// Feature 018 T1-08 / R-01 closes the gap where commit 4 only built a
// protobuf stub. Tests:
//   - TestMediaTooLarge32200 (unit, payload >16 MiB returns
//     domain.ErrMessageTooLarge mapped to -32201 MediaTooLarge at the
//     socket boundary).
//   - TestSendMediaRoundTrip (integration, WA_INTEGRATION=1) in
//     adapter_integration_test.go.
func buildMediaMessage(ctx context.Context, client whatsmeowClient, m domain.MediaMessage) (*waE2E.Message, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	payload, err := resolveMediaPayload(m)
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
func resolveMediaPayload(m domain.MediaMessage) ([]byte, error) {
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
		// TODO(spec-197): load the payload from the media store by sha256.
		// The whatsmeow Adapter does not currently hold an app.MediaStore
		// reference (buildMediaMessage only receives the whatsmeow client),
		// and threading the store through Adapter/openWithClient/Open and
		// every composition-root caller exceeds this PR's backend-plumbing
		// scope. The domain seam + sendMedia params already accept and
		// validate SHA256, so a follow-up PR only needs to wire the store
		// read here. Until then, fail loudly rather than silently dropping.
		return nil, fmt.Errorf("media sha256 source not yet wired (spec 197 follow-up): %w",
			domain.ErrMediaUnsupported)

	default:
		// Path source — original daemon-filesystem behaviour, unchanged.
		return readMediaFile(m)
	}
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
