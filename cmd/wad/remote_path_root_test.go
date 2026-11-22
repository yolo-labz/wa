package main

import (
	"context"
	"encoding/json"
	"go/ast"
	"go/parser"
	"go/token"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/adrg/xdg"

	"github.com/yolo-labz/wa/v2/internal/adapters/secondary/memory"
	"github.com/yolo-labz/wa/v2/internal/app"
	"github.com/yolo-labz/wa/v2/internal/domain"
)

// TestRESTSendMedia_RootIsTheWiredMediaRoot: run() cannot be driven from a
// test, so main.go's NewDispatcher literal is pinned in the AST (dropping
// RemotePathRoot refuses every network caller, silently); then a REST
// caller's sendMedia is checked against that root: outside refused, inside sent.
func TestRESTSendMedia_RootIsTheWiredMediaRoot(t *testing.T) {
	file, err := parser.ParseFile(token.NewFileSet(), "main.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	wired := false
	ast.Inspect(file, func(n ast.Node) bool {
		kv, ok := n.(*ast.KeyValueExpr)
		if !ok {
			return true
		}
		if key, ok := kv.Key.(*ast.Ident); ok && key.Name == "RemotePathRoot" {
			if c, ok := kv.Value.(*ast.CallExpr); ok {
				sel, ok := c.Fun.(*ast.SelectorExpr)
				wired = ok && sel.Sel.Name == "MediaRoot"
			}
		}
		return true
	})
	if !wired {
		t.Fatal("main.go does not set DispatcherConfig.RemotePathRoot from resolver.MediaRoot()")
	}
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	xdg.Reload()
	t.Cleanup(xdg.Reload)
	root := (&PathResolver{profile: "default"}).MediaRoot()
	inside, outside := filepath.Join(root, "ab12.jpg"), filepath.Join(t.TempDir(), "secret.jpg")
	_ = os.MkdirAll(root, 0o700)
	for _, p := range []string{inside, outside} {
		if err := os.WriteFile(p, []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	mem := memory.New(nil)
	d := app.NewDispatcher(app.DispatcherConfig{
		Sender: mem, Events: mem, Contacts: mem, Groups: mem, Session: mem, Allowlist: mem, Audit: mem,
		History: mem, Pairer: mem, SessionCreated: time.Now().Add(-30 * 24 * time.Hour), RemotePathRoot: root, Logger: slog.New(slog.DiscardHandler),
	})
	t.Cleanup(func() { _ = d.Close() })
	mem.Grant(domain.MustJID("5511999999999@s.whatsapp.net"), domain.ActionSend)
	send := func(p string) error { // the ctx rest/server.go gives /v1/rpc
		params, _ := json.Marshal(map[string]string{"to": "5511999999999", "path": p})
		_, err := d.Handle(app.WithRemoteCaller(context.Background(), app.TransportREST), "sendMedia", params)
		return err
	}
	if err := send(outside); err == nil || len(mem.Sent()) != 0 {
		t.Fatalf("outside the root: err=%v, sent %d; want refused", err, len(mem.Sent()))
	}
	if err := send(inside); err != nil || len(mem.Sent()) != 1 {
		t.Fatalf("inside the root: err=%v, sent %d; want one send", err, len(mem.Sent()))
	}
}
