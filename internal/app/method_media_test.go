package app

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/yolo-labz/wa/v2/internal/domain"
)

type fakeMedia struct {
	resolveCalled  bool
	resolveErr     error
	downloadCalled bool
	gcCalled       bool
	lastCutoff     time.Time
}

func (f *fakeMedia) Resolve(ctx context.Context, sha [32]byte) (domain.MediaObject, error) {
	f.resolveCalled = true
	if f.resolveErr != nil {
		return domain.MediaObject{}, f.resolveErr
	}
	return domain.MediaObject{
		Ref:          domain.MediaRef{SHA256: sha, Mime: "image/jpeg", Size: 1, Ext: "jpg"},
		Path:         "/tmp/media/aa/bb.jpg",
		MimeDetected: "image/jpeg",
	}, nil
}

func (f *fakeMedia) Download(ctx context.Context, id domain.MessageID, transcribe bool) (DownloadReport, error) {
	f.downloadCalled = true
	return DownloadReport{Cached: true}, nil
}

func (f *fakeMedia) Write(ctx context.Context, ref domain.MediaRef, payload []byte, advertisedMime string, duration int64) (domain.MediaObject, error) {
	return domain.MediaObject{}, nil
}

func (f *fakeMedia) GC(ctx context.Context, cutoff time.Time, dryRun bool) (GCReport, error) {
	f.gcCalled = true
	f.lastCutoff = cutoff
	return GCReport{Candidates: 3, Deleted: 0, BytesFreed: 0, DryRun: dryRun}, nil
}

func dispatchWithMedia(m MediaStore) *Dispatcher {
	return &Dispatcher{media: m}
}

func TestMediaResolveValidates64HexChars(t *testing.T) {
	d := dispatchWithMedia(&fakeMedia{})
	_, err := d.handleMediaResolve(context.Background(), json.RawMessage(`{"sha256":"too-short"}`))
	if !errors.Is(err, ErrInvalidParams) {
		t.Fatalf("got %v want ErrInvalidParams", err)
	}
}

func TestMediaResolveOK(t *testing.T) {
	m := &fakeMedia{}
	d := dispatchWithMedia(m)
	sha := hex.EncodeToString(make([]byte, 32))
	raw := json.RawMessage(`{"sha256":"` + sha + `"}`)
	out, err := d.handleMediaResolve(context.Background(), raw)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if !m.resolveCalled {
		t.Fatalf("resolve not called")
	}
	if !strings.Contains(string(out), `"sha256":"`+sha+`"`) {
		t.Fatalf("result: %s", out)
	}
}

func TestMediaDownloadRequiresID(t *testing.T) {
	d := dispatchWithMedia(&fakeMedia{})
	_, err := d.handleMediaDownload(context.Background(), json.RawMessage(`{}`))
	if !errors.Is(err, ErrInvalidParams) {
		t.Fatalf("err: %v", err)
	}
}

func TestMediaGCDefaultWindow(t *testing.T) {
	m := &fakeMedia{}
	d := dispatchWithMedia(m)
	if _, err := d.handleMediaGC(context.Background(), nil); err != nil {
		t.Fatalf("gc: %v", err)
	}
	if !m.gcCalled {
		t.Fatalf("gc not called")
	}
	age := time.Since(m.lastCutoff)
	if age < 29*24*time.Hour || age > 31*24*time.Hour {
		t.Fatalf("cutoff age %v not ~30d", age)
	}
}

func TestMediaGCRejectsZeroAge(t *testing.T) {
	d := dispatchWithMedia(&fakeMedia{})
	_, err := d.handleMediaGC(context.Background(), json.RawMessage(`{"olderThanSeconds":0}`))
	if !errors.Is(err, ErrInvalidParams) {
		t.Fatalf("err: %v", err)
	}
}
