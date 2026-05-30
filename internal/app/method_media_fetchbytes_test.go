package app

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/yolo-labz/wa/v2/internal/domain"
)

// fakeByteMedia is a MediaStore whose Resolve points at a real on-disk file so
// handleMediaFetchBytes can read actual bytes. size overrides the reported
// Ref.Size when non-zero (to exercise the oversize guard without writing a
// 50 MiB file); otherwise it is the real file length.
type fakeByteMedia struct {
	sha    [32]byte
	path   string
	size   int64
	resErr error
	calls  int
}

func (f *fakeByteMedia) Resolve(_ context.Context, sha [32]byte) (domain.MediaObject, error) {
	f.calls++
	if f.resErr != nil {
		return domain.MediaObject{}, f.resErr
	}
	size := f.size
	if size == 0 {
		info, err := os.Stat(f.path)
		if err != nil {
			return domain.MediaObject{}, err
		}
		size = info.Size()
	}
	return domain.MediaObject{
		Ref:          domain.MediaRef{SHA256: sha, Mime: "application/octet-stream", Size: size, Ext: "bin"},
		Path:         f.path,
		MimeDetected: "application/octet-stream",
	}, nil
}

func (f *fakeByteMedia) Download(context.Context, domain.MessageID, bool) (DownloadReport, error) {
	return DownloadReport{}, errors.New("not implemented")
}

func (f *fakeByteMedia) Write(context.Context, domain.MediaRef, []byte, string, int64) (domain.MediaObject, error) {
	return domain.MediaObject{}, errors.New("not implemented")
}

func (f *fakeByteMedia) GC(context.Context, time.Time, bool) (GCReport, error) {
	return GCReport{}, errors.New("not implemented")
}

// writeFakeMedia writes payload to a temp file and returns a store that serves
// it under its real sha256.
func writeFakeMedia(t *testing.T, payload []byte) *fakeByteMedia {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "object.bin")
	if err := os.WriteFile(path, payload, 0o600); err != nil {
		t.Fatalf("write fake media: %v", err)
	}
	return &fakeByteMedia{sha: sha256.Sum256(payload), path: path}
}

func fetchBytes(t *testing.T, d *Dispatcher, sha string, offset, length int64) mediaFetchBytesResult {
	t.Helper()
	raw, _ := json.Marshal(map[string]any{"sha256": sha, "offset": offset, "length": length})
	out, err := d.handleMediaFetchBytes(context.Background(), raw)
	if err != nil {
		t.Fatalf("fetchBytes(off=%d,len=%d): %v", offset, length, err)
	}
	var res mediaFetchBytesResult
	if uerr := json.Unmarshal(out, &res); uerr != nil {
		t.Fatalf("unmarshal result: %v", uerr)
	}
	return res
}

// TestMediaFetchBytesSingleChunk: an object smaller than the chunk ceiling is
// returned whole with eof=true and the correct schema/size/offset.
func TestMediaFetchBytesSingleChunk(t *testing.T) {
	payload := []byte("the quick brown fox jumps over the lazy dog")
	m := writeFakeMedia(t, payload)
	d := dispatchWithMedia(m)
	sha := hex.EncodeToString(m.sha[:])

	res := fetchBytes(t, d, sha, 0, 0)
	if res.Schema != mediaFetchBytesSchema {
		t.Fatalf("schema = %q want %q", res.Schema, mediaFetchBytesSchema)
	}
	if res.SHA256 != sha {
		t.Fatalf("sha = %q want %q", res.SHA256, sha)
	}
	if res.Size != int64(len(payload)) {
		t.Fatalf("size = %d want %d", res.Size, len(payload))
	}
	if res.Offset != 0 {
		t.Fatalf("offset = %d want 0", res.Offset)
	}
	if !res.EOF {
		t.Fatalf("expected eof for a single-chunk object")
	}
	if !bytes.Equal(res.Bytes, payload) {
		t.Fatalf("bytes mismatch:\n got %q\nwant %q", res.Bytes, payload)
	}
}

// TestMediaFetchBytesMultiChunkReconstructs: an object larger than the chunk
// ceiling is pulled in a loop (offset += len until eof) and reassembles to the
// exact original bytes, with eof set only on the final chunk.
func TestMediaFetchBytesMultiChunkReconstructs(t *testing.T) {
	// 2.5 chunks worth of deterministic-but-varied bytes.
	payload := make([]byte, MediaFetchBytesChunkBytes*2+MediaFetchBytesChunkBytes/2)
	for i := range payload {
		payload[i] = byte(i*7 + 3)
	}
	m := writeFakeMedia(t, payload)
	d := dispatchWithMedia(m)
	sha := hex.EncodeToString(m.sha[:])

	var got bytes.Buffer
	var offset int64
	chunks := 0
	for {
		res := fetchBytes(t, d, sha, offset, MediaFetchBytesChunkBytes)
		chunks++
		if res.Size != int64(len(payload)) {
			t.Fatalf("chunk %d size = %d want %d", chunks, res.Size, len(payload))
		}
		if res.Offset != offset {
			t.Fatalf("chunk %d offset = %d want %d", chunks, res.Offset, offset)
		}
		if len(res.Bytes) > MediaFetchBytesChunkBytes {
			t.Fatalf("chunk %d returned %d bytes, exceeds ceiling %d", chunks, len(res.Bytes), MediaFetchBytesChunkBytes)
		}
		got.Write(res.Bytes)
		offset += int64(len(res.Bytes))
		if res.EOF {
			break
		}
		if chunks > 10 {
			t.Fatalf("loop did not terminate")
		}
	}
	if chunks != 3 {
		t.Fatalf("expected 3 chunks for 2.5×ceiling payload, got %d", chunks)
	}
	if !bytes.Equal(got.Bytes(), payload) {
		t.Fatalf("reconstructed bytes differ from original (len got=%d want=%d)", got.Len(), len(payload))
	}
}

// TestMediaFetchBytesMidOffset: a non-zero offset returns the correct window
// and clamps a too-large requested length to what remains.
func TestMediaFetchBytesMidOffset(t *testing.T) {
	payload := []byte("0123456789abcdef")
	m := writeFakeMedia(t, payload)
	d := dispatchWithMedia(m)
	sha := hex.EncodeToString(m.sha[:])

	res := fetchBytes(t, d, sha, 10, 1000) // ask for more than remains
	if !res.EOF {
		t.Fatalf("offset 10 of 16 should reach eof")
	}
	if !bytes.Equal(res.Bytes, payload[10:]) {
		t.Fatalf("window mismatch: got %q want %q", res.Bytes, payload[10:])
	}
}

// TestMediaFetchBytesOffsetAtEOF: offset == size yields an empty, eof=true
// terminal chunk (valid end-of-stream poll, not an error).
func TestMediaFetchBytesOffsetAtEOF(t *testing.T) {
	payload := []byte("abc")
	m := writeFakeMedia(t, payload)
	d := dispatchWithMedia(m)
	sha := hex.EncodeToString(m.sha[:])

	res := fetchBytes(t, d, sha, int64(len(payload)), 0)
	if !res.EOF {
		t.Fatalf("offset==size should be eof")
	}
	if len(res.Bytes) != 0 {
		t.Fatalf("offset==size should return no bytes, got %d", len(res.Bytes))
	}
	if res.Size != int64(len(payload)) {
		t.Fatalf("size = %d want %d", res.Size, len(payload))
	}
}

// TestMediaFetchBytesRejectsBadSHA: a non-64-hex sha is ErrInvalidParams.
func TestMediaFetchBytesRejectsBadSHA(t *testing.T) {
	d := dispatchWithMedia(writeFakeMedia(t, []byte("x")))
	_, err := d.handleMediaFetchBytes(context.Background(), json.RawMessage(`{"sha256":"nope"}`))
	if !errors.Is(err, ErrInvalidParams) {
		t.Fatalf("got %v want ErrInvalidParams", err)
	}
}

// TestMediaFetchBytesRejectsNegative: negative offset/length is ErrInvalidParams.
func TestMediaFetchBytesRejectsNegative(t *testing.T) {
	m := writeFakeMedia(t, []byte("x"))
	d := dispatchWithMedia(m)
	sha := hex.EncodeToString(m.sha[:])
	for _, body := range []string{
		`{"sha256":"` + sha + `","offset":-1}`,
		`{"sha256":"` + sha + `","length":-5}`,
	} {
		if _, err := d.handleMediaFetchBytes(context.Background(), json.RawMessage(body)); !errors.Is(err, ErrInvalidParams) {
			t.Fatalf("body %s: got %v want ErrInvalidParams", body, err)
		}
	}
}

// TestMediaFetchBytesOversizeRejected: a cached object reporting a size above
// the 50 MiB inbound ceiling is refused with ErrMessageTooLarge (store
// corruption guard) without reading the file.
func TestMediaFetchBytesOversizeRejected(t *testing.T) {
	m := writeFakeMedia(t, []byte("small file, lying size"))
	m.size = domain.MaxInboundMediaBytes + 1
	d := dispatchWithMedia(m)
	sha := hex.EncodeToString(m.sha[:])

	_, err := d.handleMediaFetchBytes(context.Background(),
		json.RawMessage(`{"sha256":"`+sha+`","offset":0,"length":1024}`))
	if !errors.Is(err, ErrMessageTooLarge) {
		t.Fatalf("got %v want ErrMessageTooLarge", err)
	}
}

// TestMediaFetchBytesResolveErrorPropagates: a Resolve failure (object absent)
// surfaces as an error, not a zero-byte success.
func TestMediaFetchBytesResolveErrorPropagates(t *testing.T) {
	m := writeFakeMedia(t, []byte("x"))
	m.resErr = os.ErrNotExist
	d := dispatchWithMedia(m)
	sha := hex.EncodeToString(m.sha[:])
	_, err := d.handleMediaFetchBytes(context.Background(),
		json.RawMessage(`{"sha256":"`+sha+`","offset":0,"length":16}`))
	if err == nil {
		t.Fatalf("expected error when Resolve fails")
	}
}

// TestMediaFetchBytesNilStore: no MediaStore wired → ErrMethodNotFound.
func TestMediaFetchBytesNilStore(t *testing.T) {
	d := &Dispatcher{}
	_, err := d.handleMediaFetchBytes(context.Background(),
		json.RawMessage(`{"sha256":"`+hex.EncodeToString(make([]byte, 32))+`"}`))
	if !errors.Is(err, ErrMethodNotFound) {
		t.Fatalf("got %v want ErrMethodNotFound", err)
	}
}
