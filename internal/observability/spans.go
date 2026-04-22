package observability

import (
	"context"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
)

// tracerName pins the instrumentation scope reported on every span.
// One scope keeps span-library cardinality flat and lets operators
// filter by `otel.scope.name = github.com/yolo-labz/wa` in backends.
const tracerName = "github.com/yolo-labz/wa"

// Attribute keys. Kept under the `wa.` prefix so operator dashboards
// can pattern-match our signals against generic OTel semconv.
const (
	AttrProfile = "wa.profile"
	AttrJID     = "wa.jid"
	AttrMethod  = "wa.method"
)

// StartSend opens a CLIENT-kind span named `wa.send`. One span per
// outbound message call (`send`, `sendMedia`, `react`). The `method`
// argument disambiguates the three on the `wa.method` attribute.
//
// JID is hashed via HashJID before being attached — raw JIDs MUST
// NOT hit an exporter.
func StartSend(ctx context.Context, profile, method, jid string) (context.Context, trace.Span) {
	return otel.Tracer(tracerName).Start(ctx, "wa.send",
		trace.WithSpanKind(trace.SpanKindClient),
		trace.WithAttributes(
			attribute.String(AttrProfile, profile),
			attribute.String(AttrMethod, method),
			attribute.String(AttrJID, HashJID(jid)),
		),
	)
}

// StartSubscribeNotification opens a PRODUCER-kind span named
// `wa.subscribe`. One span per notification delivered out of the
// event bridge — lets operators spot fan-out-to-waiter latency
// and the depth of the `out` channel tail.
func StartSubscribeNotification(ctx context.Context, profile, eventType string) (context.Context, trace.Span) {
	return otel.Tracer(tracerName).Start(ctx, "wa.subscribe",
		trace.WithSpanKind(trace.SpanKindProducer),
		trace.WithAttributes(
			attribute.String(AttrProfile, profile),
			attribute.String(AttrMethod, eventType),
		),
	)
}

// StartHistoryBatch opens an INTERNAL-kind span named `wa.history`
// around one history-sync batch. `kind` is the whatsmeow
// `HistorySyncType` (e.g. `INITIAL_BOOTSTRAP`, `RECENT`, `PUSH_NAME`)
// so operators can tell a 5-second recent sync from a 5-minute
// full bootstrap at a glance.
func StartHistoryBatch(ctx context.Context, profile, kind string) (context.Context, trace.Span) {
	return otel.Tracer(tracerName).Start(ctx, "wa.history",
		trace.WithSpanKind(trace.SpanKindInternal),
		trace.WithAttributes(
			attribute.String(AttrProfile, profile),
			attribute.String(AttrMethod, kind),
		),
	)
}

// StartMedia opens an INTERNAL-kind span named `wa.media` around a
// media.resolve / media.download / media.gc call. `method` carries
// the JSON-RPC method name so the three paths can be separated on
// the backend without inflating span-name cardinality.
func StartMedia(ctx context.Context, profile, method string) (context.Context, trace.Span) {
	return otel.Tracer(tracerName).Start(ctx, "wa.media",
		trace.WithSpanKind(trace.SpanKindInternal),
		trace.WithAttributes(
			attribute.String(AttrProfile, profile),
			attribute.String(AttrMethod, method),
		),
	)
}
