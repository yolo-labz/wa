//go:build integration
// +build integration

// Package main is the manual integration harness for feature 003 (T035).
//
// This is NOT a CLI binary, NOT under cmd/, and NOT compiled by
// `go build ./...` because of the //go:build integration tag. It exists
// so a maintainer holding a paired WhatsApp burner number can drive the
// real Adapter.Pair flow end-to-end without needing the daemon, the
// JSON-RPC socket, or any of the cmd/wad composition root.
//
// Build:
//
//	go build -tags integration ./internal/adapters/secondary/whatsmeow/internal/pairtest/...
//
// Run (once commits 6 and 7 land — see body comment):
//
//	WA_INTEGRATION=1 go run -tags integration \
//	    ./internal/adapters/secondary/whatsmeow/internal/pairtest -phone ""
//
// The harness writes the QR code (or linking code) to stderr, blocks
// until pairing succeeds or Ctrl-C, then exits. Per spec FR-016 and the
// "Pairing Harness" Key Entity definition.
package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
)

func main() {
	phone := flag.String("phone", "", "E.164 phone for the phone-pairing-code flow; empty for QR-in-terminal")
	flag.Parse()

	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))
	_ = logger

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	_ = ctx
	_ = phone

	// Placeholder: real wiring requires sqlitestore + sqlitehistory + whatsmeow
	// adapter. Feature 006 (cmd/wad composition root) will subsume this harness.
	// Until then, compilation verifies import correctness only.
	fmt.Fprintln(os.Stderr, "pairtest: not yet wired — composition root lands in feature 006")
	os.Exit(0)
}
