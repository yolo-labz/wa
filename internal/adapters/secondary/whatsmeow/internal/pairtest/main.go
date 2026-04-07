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

	// TODO(commit 6/7): construct real sqlitestore + sqlitehistory; for
	// now the harness compiles only — the runtime path depends on
	// packages that land in commits 6 and 7.
	fmt.Fprintln(os.Stderr, "pairtest: NOT YET RUNNABLE — sqlitestore + sqlitehistory land in commits 6 and 7")
	fmt.Fprintln(os.Stderr, "pairtest: build only verifies compile correctness today")
	os.Exit(0)

	// Once commits 6 and 7 land, the maintainer will replace the early
	// exit above with the wiring sketched below:
	//
	//   sessionPath := filepath.Join(os.Getenv("HOME"), ".local/share/wa/session.db")
	//   historyPath := filepath.Join(os.Getenv("HOME"), ".local/share/wa/messages.db")
	//   ssn, err := sqlitestore.Open(ctx, sessionPath)  // commit 6
	//   if err != nil { logger.Error("session open failed", "err", err); os.Exit(1) }
	//   defer ssn.Close()
	//   hist, err := sqlitehistory.Open(ctx, historyPath)  // commit 7
	//   if err != nil { logger.Error("history open failed", "err", err); os.Exit(1) }
	//   defer hist.Close()
	//   allowlist := domain.NewAllowlist()
	//   adapter, err := whatsmeow.Open(ctx, ssn, hist, allowlist, logger)
	//   if err != nil { logger.Error("adapter open failed", "err", err); os.Exit(1) }
	//   defer adapter.Close()
	//   if err := adapter.Pair(ctx, *phone); err != nil {
	//       logger.Error("pair failed", "err", err); os.Exit(1)
	//   }
	//   fmt.Fprintln(os.Stderr, "paired ok; press Ctrl-C to exit")
	//   <-ctx.Done()
}
