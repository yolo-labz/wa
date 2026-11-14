package app

import "context"

// QRSink receives each pairing QR payload in place of the terminal and
// temp-file renderers (spec 116 SR-116-06). The payload is a credential
// for the next minute: an implementation must not log, cache or persist it.
type QRSink func(code string)

type qrSinkKey struct{}

// WithQRSink returns ctx carrying sink, so the pair flow the job runs
// under ctx sends its codes to sink alone.
func WithQRSink(ctx context.Context, sink QRSink) context.Context {
	return context.WithValue(ctx, qrSinkKey{}, sink)
}

// QRSinkFrom returns the sink ctx carries, or nil (the CLI path).
func QRSinkFrom(ctx context.Context) QRSink {
	sink, _ := ctx.Value(qrSinkKey{}).(QRSink)
	return sink
}
