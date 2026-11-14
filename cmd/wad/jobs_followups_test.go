package main

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"testing/synctest"

	"github.com/yolo-labz/wa/v2/internal/app"
)

// TestJobs_UnattributedCallersDoNotShareOwner (p8 S4c1 L2): a call no
// transport attributed owns its jobs alone. Two such callers sharing one
// non-admin owner could poll and cancel each other's jobs.
func TestJobs_UnattributedCallersDoNotShareOwner(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		p := &blockingPairer{started: make(chan bool, 1), stopped: make(chan error, 1)}
		da, _ := jobsAdapter(t, p, nil)

		job := mustRPC(t, da, "pair", `{"async":true}`) // no app.WithCaller on the ctx
		<-p.started
		for _, op := range []string{"jobs.get", "jobs.cancel"} {
			if _, err := da.Handle(t.Context(), op, []byte(jobArg(job["job_id"]))); !errors.Is(err, app.ErrJobNotFound) {
				t.Errorf("%s from another unattributed caller: err = %v, want %v", op, err, app.ErrJobNotFound)
			}
		}
		select {
		case err := <-p.stopped:
			t.Fatalf("another caller's jobs.cancel stopped the pairing worker: %v", err)
		default:
		}
	})
}

// detachScanDirs are the packages a handler's work runs in.
var detachScanDirs = []string{"internal/app", "internal/adapters/secondary/whatsmeow", "cmd/wad"}

// detachAllowed lists the files that may root or detach a context, each
// with its reason: lifecycle roots, event-path work that outlives any one
// request, and CLI subcommands that run no handler.
var detachAllowed = map[string]string{
	"internal/app/dispatcher.go":                                 "lifecycle root",
	"internal/app/eventbridge.go":                                "lifecycle root",
	"internal/app/executor.go":                                   "lifecycle root",
	"internal/adapters/secondary/whatsmeow/adapter.go":           "client connection lifetime",
	"internal/adapters/secondary/whatsmeow/recovery_backfill.go": "FR-116-24 worker: daemon lifetime, not a request",
	// a.clientCtx for work no request owns: inbound events, receipts, presence.
	"internal/adapters/secondary/whatsmeow/adapter_inbound.go":  "event path",
	"internal/adapters/secondary/whatsmeow/receipts.go":         "event path",
	"internal/adapters/secondary/whatsmeow/presence_offline.go": "shutdown presence",
	"cmd/wad/main.go":        "daemon lifecycle roots",
	"cmd/wad/cleanup.go":     "shutdown deadline roots",
	"cmd/wad/rest_http.go":   "listener lifecycle",
	"cmd/wad/health_http.go": "listener lifecycle",
	"cmd/wad/events_pump.go": "event pump lifecycle",
	"cmd/wad/audit_cmd.go":   "CLI subcommand",
	"cmd/wad/migrate.go":     "CLI subcommand",
	"cmd/wad/reload.go":      "CLI subcommand",
	"cmd/wad/token_cmd.go":   "CLI subcommand",
}

// detach finds a context rooted at Background, stripped of cancellation,
// or the adapter's client context used as a work context: only a bare
// .Done()/.Err() watch of clientCtx is request-safe.
var detach = regexp.MustCompile(`context\.With(?:Timeout|Deadline|Cancel)\(context\.Background\(\)|context\.WithoutCancel\(|\ba\.clientCtx\b(\.Done\(\)|\.Err\(\))?`)

// detachedContexts returns "file:line" for each such use in the non-test
// sources of dirs (under root) outside allowed.
func detachedContexts(root string, dirs []string, allowed map[string]string) []string {
	var hits []string
	for _, dir := range dirs {
		files, _ := filepath.Glob(filepath.Join(root, dir, "*.go"))
		for _, f := range files {
			rel := strings.TrimPrefix(filepath.ToSlash(f), filepath.ToSlash(root)+"/")
			src, err := os.ReadFile(f)
			if err != nil || strings.HasSuffix(f, "_test.go") || allowed[rel] != "" {
				continue
			}
			for _, m := range detach.FindAllSubmatchIndex(src, -1) {
				if m[2] < 0 { // not a clientCtx .Done()/.Err() watch
					hits = append(hits, rel+":"+strconv.Itoa(bytes.Count(src[:m[0]], []byte("\n"))+1))
				}
			}
		}
	}
	return hits
}

// TestDetachScanner (p8 S4c1 L3, I3): the scanner sees a send on
// clientCtx and a Background root in cmd/wad, and the tree is clean.
func TestDetachScanner(t *testing.T) {
	root := t.TempDir()
	for name, src := range map[string]string{
		"cmd/wad/h.go": "package main\nvar _ = context.WithTimeout(context.Background(), 1)\n",
		"internal/adapters/secondary/whatsmeow/h.go": "package w\nfunc f() {\n\ta.client.SendMessage(a.clientCtx, x, y)\n\t<-a.clientCtx.Done()\n}\n",
		"internal/app/ok.go":                         "package app\n",
	} {
		_ = os.MkdirAll(filepath.Dir(filepath.Join(root, name)), 0o755)
		_ = os.WriteFile(filepath.Join(root, name), []byte(src), 0o600)
	}
	got := strings.Join(detachedContexts(root, detachScanDirs, nil), " ")
	if want := "internal/adapters/secondary/whatsmeow/h.go:3 cmd/wad/h.go:2"; got != want {
		t.Errorf("scanner hits = %q, want %q (a clientCtx send and a Background root; the Done() watch is fine)", got, want)
	}
	if hits := detachedContexts("../..", detachScanDirs, detachAllowed); len(hits) != 0 {
		t.Errorf("unallowlisted detached contexts: %v", hits)
	}
}
