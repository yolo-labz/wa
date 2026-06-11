package socket

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"regexp"
	"testing"

	"github.com/creachadair/jrpc2"
)

// panicDispatcher panics on every Handle call.
type panicDispatcher struct{}

func (panicDispatcher) Handle(context.Context, string, json.RawMessage) (json.RawMessage, error) {
	panic("kaboom: nil map write")
}

func (panicDispatcher) Events() <-chan Event { return nil }

// okDispatcher echoes a fixed result.
type okDispatcher struct{}

func (okDispatcher) Handle(context.Context, string, json.RawMessage) (json.RawMessage, error) {
	return json.RawMessage(`{"ok":true}`), nil
}

func (okDispatcher) Events() <-chan Event { return nil }

var panicRefMsgRe = regexp.MustCompile(`^Internal error \(ref ([0-9a-f]{8})\)$`)

// TestDispatchPanicCorrelationRef pins the SF-09 contract: a panic in
// the dispatcher surfaces as -32603 with a correlation ref, the same
// ref lands in the server log next to the stack, and neither the panic
// value nor the stack crosses the wire.
func TestDispatchPanicCorrelationRef(t *testing.T) {
	var logBuf bytes.Buffer
	log := slog.New(slog.NewTextHandler(&logBuf, nil))
	s := NewServer(panicDispatcher{}, log)

	result, err := s.dispatchRecovered(context.Background(), "send", "42", nil)
	if result != nil {
		t.Errorf("result = %v, want nil after panic", result)
	}
	var jerr *jrpc2.Error
	if !errors.As(err, &jerr) {
		t.Fatalf("err = %T (%v), want *jrpc2.Error", err, err)
	}
	if int(jerr.Code) != int(CodeInternalError) {
		t.Errorf("code = %d, want %d", jerr.Code, CodeInternalError)
	}

	m := panicRefMsgRe.FindStringSubmatch(jerr.Message)
	if m == nil {
		t.Fatalf("message %q does not match %q", jerr.Message, panicRefMsgRe)
	}
	ref := m[1]

	logged := logBuf.String()
	if !bytes.Contains(logBuf.Bytes(), []byte(ref)) {
		t.Errorf("log does not contain wire ref %q:\n%s", ref, logged)
	}
	for _, want := range []string{"panic in dispatcher", "method=send", "requestID=42", "stack="} {
		if !bytes.Contains(logBuf.Bytes(), []byte(want)) {
			t.Errorf("log missing %q:\n%s", want, logged)
		}
	}

	// Trust boundary: panic value and stack stay server-side.
	for _, leak := range []string{"kaboom", "nil map write", "goroutine"} {
		if bytes.Contains([]byte(jerr.Message), []byte(leak)) {
			t.Errorf("wire message %q leaks %q", jerr.Message, leak)
		}
	}
}

// TestDispatchRecoveredPassthrough asserts the non-panic path is
// unchanged by the SF-09 refactor: results and nil flow through as
// before.
func TestDispatchRecoveredPassthrough(t *testing.T) {
	s := NewServer(okDispatcher{}, slog.New(slog.DiscardHandler))

	result, err := s.dispatchRecovered(context.Background(), "send", "1", nil)
	if err != nil {
		t.Fatalf("dispatchRecovered: %v", err)
	}
	raw, ok := result.(json.RawMessage)
	if !ok {
		t.Fatalf("result = %T, want json.RawMessage", result)
	}
	if string(raw) != `{"ok":true}` {
		t.Errorf("result = %s, want {\"ok\":true}", raw)
	}
}
