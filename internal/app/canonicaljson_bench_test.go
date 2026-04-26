package app

import (
	"strings"
	"testing"
)

// Baseline benchmarks for the canonicalJSON / CanonicalParamsHash hot
// path used by the FR-033 idempotency gate. canonicalJSON does three
// JSON round-trips per call (Marshal → Unmarshal → marshalCanonical),
// so the dispatcher pays this cost on every mutating method whose
// params include `idempotencyKey`. Keep these benches stable —
// regressions show up in `benchstat` against a `go test -bench=. -count=10`
// baseline checked into `~/Documents/Notes/wa-improvement-loop/profiling/`.

// Representative `send` params payload — small map, mixed value types,
// closest to the real on-the-wire shape (~120 B raw JSON).
var benchSendParams = map[string]any{
	"to":             "5511999999999@s.whatsapp.net",
	"body":           "hello world",
	"idempotencyKey": "01JXY9ZZZ0000000000000000A",
	"ts":             1714000000,
	"requestId":      "req-0001",
	"replyTo":        "ABCDEF1234567890",
}

// Larger params used by `sendMedia` — includes a small base64 thumbnail
// stand-in so the canonicalJSON path exercises a wider key space.
var benchSendMediaParams = map[string]any{
	"to":             "5511999999999@s.whatsapp.net",
	"path":           "/tmp/media/photo.jpg",
	"caption":        strings.Repeat("a", 256),
	"mime":           "image/jpeg",
	"idempotencyKey": "01JXY9ZZZ0000000000000000B",
	"ts":             1714000000,
	"meta": map[string]any{
		"width":     4032,
		"height":    3024,
		"thumbHash": "1QcSHQRnh493V4MIh4eXh7eIyCZ2YBBgZBhGZmZmZmZmZmZm",
	},
}

func BenchmarkCanonicalJSON_Send(b *testing.B) {
	b.ReportAllocs()
	for range b.N {
		if _, err := canonicalJSON(benchSendParams); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkCanonicalJSON_SendMedia(b *testing.B) {
	b.ReportAllocs()
	for range b.N {
		if _, err := canonicalJSON(benchSendMediaParams); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkCanonicalParamsHash_Send(b *testing.B) {
	b.ReportAllocs()
	for range b.N {
		if _, err := CanonicalParamsHash(benchSendParams); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkCanonicalParamsHash_SendMedia(b *testing.B) {
	b.ReportAllocs()
	for range b.N {
		if _, err := CanonicalParamsHash(benchSendMediaParams); err != nil {
			b.Fatal(err)
		}
	}
}
