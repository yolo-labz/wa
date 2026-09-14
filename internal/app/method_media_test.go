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
	downloadErr    error
	downloadObject domain.MediaObject
	gcCalled       bool
	lastCutoff     time.Time
	lastChat       domain.JID
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

func (f *fakeMedia) Download(ctx context.Context, chat domain.JID, id domain.MessageID, transcribe bool) (DownloadReport, error) {
	f.downloadCalled = true
	f.lastChat = chat
	return DownloadReport{Object: f.downloadObject, Cached: true, Chat: chat, MessageID: id}, f.downloadErr
}

func TestMediaDownloadValidatesAndBindsChat(t *testing.T) {
	m := &fakeMedia{}
	d := dispatchWithMedia(m)
	if _, err := d.handleMediaDownload(context.Background(), json.RawMessage(`{"chat":"bad jid","messageId":"M1"}`)); !errors.Is(err, ErrInvalidJID) {
		t.Fatalf("invalid chat error = %v", err)
	}
	if m.downloadCalled {
		t.Fatal("invalid chat reached media port")
	}
	for _, id := range []string{"", "bad id", "</channel>", strings.Repeat("A", 65)} {
		raw, _ := json.Marshal(map[string]any{"messageId": id})
		if _, err := d.handleMediaDownload(context.Background(), raw); !errors.Is(err, ErrInvalidParams) || m.downloadCalled {
			t.Fatalf("unsafe ID reached port: %q, %v", id, err)
		}
	}
	out, err := d.handleMediaDownload(context.Background(), json.RawMessage(`{"chat":"5511999999999@s.whatsapp.net","messageId":"M1"}`))
	if err != nil {
		t.Fatalf("download: %v", err)
	}
	if got := m.lastChat.String(); got != "5511999999999@s.whatsapp.net" {
		t.Fatalf("port chat = %q", got)
	}
	if !strings.Contains(string(out), `"selection":{"chatJid":"5511999999999@s.whatsapp.net","messageId":"M1"}`) {
		t.Fatalf("response is not bound to selection: %s", out)
	}
}

func TestMediaAmbiguityNeverReachesTranscription(t *testing.T) {
	m := &fakeMedia{
		downloadErr:    domain.ErrMessageIDAmbiguous,
		downloadObject: domain.MediaObject{MimeDetected: "audio/ogg", Path: "/must-not-transcribe"},
	}
	d := dispatchWithMedia(m)
	transcriber := &fakeTranscriber{}
	d.transcriber = transcriber
	out, err := d.handleMediaDownload(context.Background(), json.RawMessage(`{"messageId":"DUP","transcribe":true}`))
	if !errors.Is(err, domain.ErrMessageIDAmbiguous) || out != nil || transcriber.called != 0 {
		t.Fatalf("ambiguous download exposed result/transcription: %s, %v, %d", out, err, transcriber.called)
	}
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
