package whatsmeow

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"os"
	"strings"
	"sync"
	"testing"

	waClient "go.mau.fi/whatsmeow"

	"github.com/yolo-labz/wa/v2/internal/app"
	"github.com/yolo-labz/wa/v2/internal/pairpath"
)

const fixtureQR = "2@FIXTUREqrRef7Qx,bm9pc2U=,aWRlbnRpdHk=,YWR2U2VjcmV0"

// captureStderr swaps os.Stderr for a pipe while fn runs.
func captureStderr(t *testing.T, fn func()) string {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	prev := os.Stderr
	os.Stderr = w
	var out bytes.Buffer
	done := make(chan struct{})
	go func() { _, _ = io.Copy(&out, r); close(done) }()
	defer func() {
		os.Stderr = prev
		_ = w.Close()
		<-done
	}()
	fn()
	_ = w.Close()
	<-done
	os.Stderr = prev
	return out.String()
}

// qrFlow feeds two codes and a success through the fake QR channel.
func qrFlow(t *testing.T, logger *slog.Logger) *Adapter {
	t.Helper()
	fake := newFakeClient()
	qr := make(chan waClient.QRChannelItem, 3)
	qr <- waClient.QRChannelItem{Event: "code", Code: fixtureQR}
	qr <- waClient.QRChannelItem{Event: "code", Code: fixtureQR + "2"}
	qr <- waClient.QRChannelItem{Event: "success"}
	close(qr)
	fake.QRChan = qr
	a := openWithClient(fake, nil, logger, nil)
	t.Cleanup(func() { _ = a.Close() })
	return a
}

// TestPair_QRSinkReplacesTerminalAndFile (SR-116-06): with a sink on the
// ctx, every code goes to the sink alone: nothing on stderr, no pair page
// in the temp dir.
func TestPair_QRSinkReplacesTerminalAndFile(t *testing.T) {
	t.Setenv("TMPDIR", t.TempDir())
	a := qrFlow(t, nil)
	var got []string
	ctx := app.WithQRSink(context.Background(), func(code string) { got = append(got, code) })
	stderr := captureStderr(t, func() {
		if err := a.pairQR(ctx); err != nil {
			t.Errorf("pairQR: %v", err)
		}
	})
	if len(got) != 2 || got[0] != fixtureQR {
		t.Errorf("sink got %q, want both codes", got)
	}
	if stderr != "" {
		t.Errorf("sink path wrote %d bytes to stderr", len(stderr))
	}
	if entries, _ := os.ReadDir(os.TempDir()); len(entries) != 0 {
		t.Errorf("sink path left files in the temp dir: %v", entries)
	}
}

// TestPair_QRWithoutSinkUnchanged: the CLI path (no sink) still renders
// to stderr and writes the pair page, ending on the paired state.
func TestPair_QRWithoutSinkUnchanged(t *testing.T) {
	t.Setenv("TMPDIR", t.TempDir())
	a := qrFlow(t, nil)
	stderr := captureStderr(t, func() {
		if err := a.pairQR(context.Background()); err != nil {
			t.Errorf("pairQR: %v", err)
		}
	})
	if stderr == "" {
		t.Error("CLI path rendered no terminal QR")
	}
	page, err := os.ReadFile(pairpath.Path(""))
	if err != nil || !strings.Contains(string(page), "Paired successfully") {
		t.Errorf("CLI pair page missing or not paired: %v", err)
	}
}

// lockedLog is a log sink shared by the adapter and whatsmeow goroutines.
type lockedLog struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (l *lockedLog) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.Write(p)
}

// TestPair_QRPayloadNeverLogged (T5): a full sink flow at debug level,
// including whatsmeow's own QR emitter log line, leaves the payload in no
// log, on no stderr and in no file.
func TestPair_QRPayloadNeverLogged(t *testing.T) {
	t.Setenv("TMPDIR", t.TempDir())
	var logs lockedLog
	logger := slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug}))
	prev := slog.Default()
	slog.SetDefault(logger)
	t.Cleanup(func() { slog.SetDefault(prev) })
	a := qrFlow(t, logger)
	sink := func(code string) {
		// what whatsmeow's QRChannel logs for each code (qrchan.go)
		NewSlogLogger(logger).Sub("QRChannel").Debugf("Emitting QR code %s", code)
	}
	stderr := captureStderr(t, func() {
		if err := a.pairQR(app.WithQRSink(context.Background(), sink)); err != nil {
			t.Errorf("pairQR: %v", err)
		}
	})
	logs.mu.Lock()
	out := logs.b.String()
	logs.mu.Unlock()
	if !strings.Contains(out, "Emitting QR code") {
		t.Fatalf("the emitter line was not logged at all:\n%s", out)
	}
	for where, s := range map[string]string{"log": out, "stderr": stderr} {
		if strings.Contains(s, "FIXTUREqrRef7Qx") {
			t.Errorf("QR payload found in %s:\n%s", where, s)
		}
	}
	if entries, _ := os.ReadDir(os.TempDir()); len(entries) != 0 {
		t.Errorf("files written: %v", entries)
	}
}
