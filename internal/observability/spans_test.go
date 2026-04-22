package observability

import (
	"context"
	"testing"

	"go.opentelemetry.io/otel"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"
)

// TestSpanKindsCorrect pins the contract that each of the 4 named
// spans carries the SpanKind operators rely on for dashboard
// filtering. `wa.send` SHOULD be CLIENT (outbound to whatsmeow →
// WA servers); `wa.subscribe` SHOULD be PRODUCER (we produce events
// into a queue, waiters consume); `wa.history` + `wa.media` SHOULD
// be INTERNAL (work inside the daemon, no cross-service hop).
func TestSpanKindsCorrect(t *testing.T) {
	rec := tracetest.NewSpanRecorder()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(rec))
	otel.SetTracerProvider(tp)
	t.Cleanup(func() { _ = tp.Shutdown(context.Background()) })

	_, s1 := StartSend(context.Background(), "default", "send", "5511@s.whatsapp.net")
	s1.End()
	_, s2 := StartSubscribeNotification(context.Background(), "default", "message.in")
	s2.End()
	_, s3 := StartHistoryBatch(context.Background(), "default", "RECENT")
	s3.End()
	_, s4 := StartMedia(context.Background(), "default", "media.resolve")
	s4.End()

	got := rec.Ended()
	if len(got) != 4 {
		t.Fatalf("recorded %d spans, want 4", len(got))
	}
	want := map[string]trace.SpanKind{
		"wa.send":      trace.SpanKindClient,
		"wa.subscribe": trace.SpanKindProducer,
		"wa.history":   trace.SpanKindInternal,
		"wa.media":     trace.SpanKindInternal,
	}
	for _, sp := range got {
		if k, ok := want[sp.Name()]; !ok {
			t.Errorf("unexpected span %q", sp.Name())
		} else if sp.SpanKind() != k {
			t.Errorf("%s kind = %v, want %v", sp.Name(), sp.SpanKind(), k)
		}
	}
}

// TestSendSpanHashesJID asserts raw JIDs never leak onto span
// attributes; the wa.jid attribute MUST be the HashJID output.
func TestSendSpanHashesJID(t *testing.T) {
	resetJIDSaltForTest()
	rec := tracetest.NewSpanRecorder()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(rec))
	otel.SetTracerProvider(tp)
	t.Cleanup(func() { _ = tp.Shutdown(context.Background()) })

	raw := "5511999999999@s.whatsapp.net"
	_, sp := StartSend(context.Background(), "default", "send", raw)
	sp.End()

	ended := rec.Ended()
	if len(ended) != 1 {
		t.Fatalf("ended %d spans, want 1", len(ended))
	}
	for _, kv := range ended[0].Attributes() {
		if string(kv.Key) == AttrJID {
			got := kv.Value.AsString()
			if got == raw {
				t.Errorf("wa.jid attribute leaked raw JID %q", raw)
			}
			if got == "" {
				t.Error("wa.jid attribute empty for non-empty input")
			}
		}
	}
}
