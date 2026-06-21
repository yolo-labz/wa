package app

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/yolo-labz/wa/v2/internal/domain"
)

// decodeMediaObjectView decodes the media.download / media.resolve envelope
// into the typed view. Asserting on the decoded mediaObjectView.Channel (vs a
// substring of the raw wire bytes) is immune to encoding/json's \uXXXX
// escaping of the envelope's < and > characters.
func decodeMediaObjectView(t *testing.T, out json.RawMessage) mediaObjectView {
	t.Helper()
	var env struct {
		Object mediaObjectView `json:"object"`
	}
	if err := json.Unmarshal(out, &env); err != nil {
		t.Fatalf("decode media object: %v (raw=%s)", err, out)
	}
	return env.Object
}

// assertTranscriptInChannel pins the FR-005a contract for a transcribed media
// view: the raw Transcript field is empty and the (decoded) Channel carries the
// transcript text inside a body field.
func assertTranscriptInChannel(t *testing.T, out json.RawMessage, want string) {
	t.Helper()
	obj := decodeMediaObjectView(t, out)
	if obj.Transcript != "" {
		t.Fatalf("raw transcript leaked to wire: %q", obj.Transcript)
	}
	if !strings.Contains(obj.Channel, `name="body"`) || !strings.Contains(obj.Channel, want) {
		t.Fatalf("channel = %q, want a body field carrying %q", obj.Channel, want)
	}
}

// fakeAudioMedia is a MediaStore stub modelling a real WhatsApp voice note:
// an Opus-in-Ogg payload that net/http.DetectContentType sniffs as the
// generic "application/ogg" container (NOT "audio/ogg"), with the advertised
// mime carrying the true "audio/ogg; codecs=opus". This is the shape that
// exposed issue #179 — a detected-only "audio/" prefix gate silently skips
// it, so these tests only pass via domain.MediaObject.IsAudio.
type fakeAudioMedia struct {
	resolveObj domain.MediaObject
}

func (f *fakeAudioMedia) Resolve(ctx context.Context, sha [32]byte) (domain.MediaObject, error) {
	obj := f.resolveObj
	if obj.Ref.SHA256 == ([32]byte{}) {
		obj.Ref = domain.MediaRef{SHA256: sha, Mime: "application/ogg", Size: 42, Ext: "ogg"}
		obj.MimeAdvertised = "audio/ogg; codecs=opus"
		obj.MimeDetected = "application/ogg"
		obj.Path = "/tmp/media/voice.ogg"
	}
	return obj, nil
}

func (f *fakeAudioMedia) Download(ctx context.Context, id domain.MessageID, transcribe bool) (DownloadReport, error) {
	sha := [32]byte{1, 2, 3}
	return DownloadReport{
		Object: domain.MediaObject{
			Ref:            domain.MediaRef{SHA256: sha, Mime: "application/ogg", Size: 42, Ext: "ogg"},
			Path:           "/tmp/media/voice.ogg",
			MimeAdvertised: "audio/ogg; codecs=opus",
			MimeDetected:   "application/ogg",
		},
		Cached:       false,
		BytesFetched: 42,
	}, nil
}

func (f *fakeAudioMedia) Write(_ context.Context, _ domain.MediaRef, _ []byte, _ string, _ int64) (domain.MediaObject, error) {
	return domain.MediaObject{}, nil
}

func (f *fakeAudioMedia) GC(_ context.Context, _ time.Time, dryRun bool) (GCReport, error) {
	return GCReport{DryRun: dryRun}, nil
}

// fakeTranscriber records the last Transcribe invocation and replays
// canned text/lang/err.
type fakeTranscriber struct {
	mu        sync.Mutex
	called    int
	lastPath  string
	lastLang  string
	replyText string
	replyLang string
	replyErr  error
}

func (f *fakeTranscriber) Transcribe(ctx context.Context, path string, lang string) (string, string, error) {
	f.mu.Lock()
	f.called++
	f.lastPath = path
	f.lastLang = lang
	f.mu.Unlock()
	return f.replyText, f.replyLang, f.replyErr
}

// fakeTranscripts is an in-memory TranscriptStore for testing the cache
// hit + miss + persist paths. Tracks Upsert invocations.
type fakeTranscripts struct {
	mu       sync.Mutex
	records  map[[32]byte]TranscriptRecord
	upserted int
	getErr   error
}

func newFakeTranscripts() *fakeTranscripts {
	return &fakeTranscripts{records: map[[32]byte]TranscriptRecord{}}
}

func (f *fakeTranscripts) Upsert(_ context.Context, rec TranscriptRecord) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.upserted++
	f.records[rec.SHA256] = rec
	return nil
}

func (f *fakeTranscripts) Get(_ context.Context, sha [32]byte) (TranscriptRecord, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.getErr != nil {
		return TranscriptRecord{}, f.getErr
	}
	return f.records[sha], nil
}

// newTranscribeDispatcher builds a Dispatcher pre-wired for media.download
// transcribe-path tests. Bridge is constructed but not Run — EmitSynthetic
// fans out synchronously into b.out, which the test drains.
func newTranscribeDispatcher(media MediaStore, t Transcriber, store TranscriptStore, name string) *Dispatcher {
	bridge := NewEventBridge(&fakeStream{}, slog.Default())
	return &Dispatcher{
		media:           media,
		transcriber:     t,
		transcripts:     store,
		transcriberName: name,
		bridge:          bridge,
		log:             slog.Default(),
	}
}

// downloadAndTranscribe drives handleMediaDownload with the canonical voice-note
// request (transcribe=true), fails the test on a transport error, and asserts
// the transcriber fired wantCalled times. It returns the wire response so the
// caller can run the FR-005a channel assertions.
func downloadAndTranscribe(t *testing.T, d *Dispatcher, tr *fakeTranscriber, wantCalled int) json.RawMessage {
	t.Helper()
	out, err := d.handleMediaDownload(context.Background(),
		json.RawMessage(`{"messageId":"m1","transcribe":true}`))
	if err != nil {
		t.Fatalf("download: %v", err)
	}
	if tr.called != wantCalled {
		t.Fatalf("transcriber called %d times, want %d", tr.called, wantCalled)
	}
	return out
}

func TestMediaDownloadTranscribeDisabledOnAudio(t *testing.T) {
	// transcribe=true, audio/*, but no Transcriber wired and no cache hit
	// → -32115 transcriber_not_configured (rule 12: no silent fallback).
	d := newTranscribeDispatcher(&fakeAudioMedia{}, nil, newFakeTranscripts(), "")
	_, err := d.handleMediaDownload(context.Background(),
		json.RawMessage(`{"messageId":"m1","transcribe":true}`))
	if !errors.Is(err, ErrTranscriberNotConfigured) {
		t.Fatalf("err = %v, want ErrTranscriberNotConfigured", err)
	}
}

func TestMediaDownloadTranscribeAdapterError(t *testing.T) {
	tr := &fakeTranscriber{replyErr: errors.New("whisper crashed")}
	d := newTranscribeDispatcher(&fakeAudioMedia{}, tr, newFakeTranscripts(), "whispercpp")
	_, err := d.handleMediaDownload(context.Background(),
		json.RawMessage(`{"messageId":"m1","transcribe":true}`))
	if !errors.Is(err, ErrTranscribeFailed) {
		t.Fatalf("err = %v, want ErrTranscribeFailed", err)
	}
	if !strings.Contains(err.Error(), "whisper crashed") {
		t.Fatalf("err = %v, want wrapped adapter cause", err)
	}
}

func TestMediaDownloadTranscribeCacheMissPopulatesAndEmits(t *testing.T) {
	tr := &fakeTranscriber{replyText: "olá pedro", replyLang: "pt"}
	store := newFakeTranscripts()
	d := newTranscribeDispatcher(&fakeAudioMedia{}, tr, store, "whispercpp")

	sub := d.bridge.Events()
	out := downloadAndTranscribe(t, d, tr, 1)
	// FR-005a: the transcript is attacker-controllable text, emitted only
	// inside the `<channel source="wa">` envelope; the raw field stays empty.
	assertTranscriptInChannel(t, out, "olá pedro")
	if store.upserted != 1 {
		t.Fatalf("upserted %d, want 1", store.upserted)
	}
	rec := store.records[[32]byte{1, 2, 3}]
	if rec.Transcript != "olá pedro" || rec.Lang != "pt" || rec.Adapter != "whispercpp" {
		t.Fatalf("persisted rec = %+v", rec)
	}
	select {
	case evt := <-sub:
		if evt.Type != "media.transcribed" {
			t.Fatalf("event type = %q, want media.transcribed", evt.Type)
		}
		mt, ok := evt.Payload.(domain.MediaTranscribedEvent)
		if !ok {
			t.Fatalf("payload type = %T", evt.Payload)
		}
		if mt.Adapter != "whispercpp" || mt.Lang != "pt" || mt.Chars != 9 {
			t.Fatalf("event payload = %+v", mt)
		}
	case <-time.After(time.Second):
		t.Fatal("no synthetic media.transcribed event")
	}
}

func TestMediaDownloadTranscribeCacheHitSkipsAdapter(t *testing.T) {
	// Pre-seed the cache so handleMediaDownload short-circuits without
	// invoking the adapter (FR-004 content-addressed dedup).
	tr := &fakeTranscriber{replyText: "should-not-be-called"}
	store := newFakeTranscripts()
	store.records[[32]byte{1, 2, 3}] = TranscriptRecord{
		SHA256:     [32]byte{1, 2, 3},
		Transcript: "previously cached",
		Lang:       "en",
		Adapter:    "groq",
	}
	d := newTranscribeDispatcher(&fakeAudioMedia{}, tr, store, "whispercpp")
	out := downloadAndTranscribe(t, d, tr, 0)
	// FR-005a: cached transcript still travels only inside the channel envelope.
	assertTranscriptInChannel(t, out, "previously cached")
}

func TestMediaDownloadTranscribeNonAudioShortCircuits(t *testing.T) {
	// Non-audio mime: transcribe=true is a no-op, no adapter invocation,
	// no -32115 error (the gate is mime-prefix-driven).
	media := &fakeMedia{}
	tr := &fakeTranscriber{}
	d := newTranscribeDispatcher(media, tr, newFakeTranscripts(), "whispercpp")
	downloadAndTranscribe(t, d, tr, 0)
}

func TestMediaDownloadTranscribeOggVoiceNote_Issue179(t *testing.T) {
	// Regression for #179: a WhatsApp voice note sniffs as the generic
	// "application/ogg" container, so the old gate (HasPrefix on the detected
	// mime alone) silently skipped transcription with no error. fakeAudioMedia
	// now returns that realistic shape; the transcriber must still fire and
	// the transcript must reach the wire.
	tr := &fakeTranscriber{replyText: "bom dia", replyLang: "pt"}
	d := newTranscribeDispatcher(&fakeAudioMedia{}, tr, newFakeTranscripts(), "whispercpp")
	out := downloadAndTranscribe(t, d, tr, 1)
	// FR-005a: transcript travels only inside the channel envelope.
	assertTranscriptInChannel(t, out, "bom dia")
}

func TestMediaResolveHydratesFromTranscriptStore(t *testing.T) {
	// FR-004: when MediaStore.Resolve returns an empty Transcript and a
	// matching record exists in the store, the dispatcher hydrates the
	// wire response from the sidecar without re-running transcription.
	sha := [32]byte{0xaa}
	store := newFakeTranscripts()
	store.records[sha] = TranscriptRecord{
		SHA256:     sha,
		Transcript: "hydrated from sidecar",
		Lang:       "pt",
		Adapter:    "whispercpp",
	}
	d := newTranscribeDispatcher(&fakeAudioMedia{}, nil, store, "")
	hexSHA := hex.EncodeToString(sha[:])
	out, err := d.handleMediaResolve(context.Background(),
		json.RawMessage(`{"sha256":"`+hexSHA+`"}`))
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	// FR-005a: hydrated transcript travels only inside the channel envelope.
	assertTranscriptInChannel(t, out, "hydrated from sidecar")
}
