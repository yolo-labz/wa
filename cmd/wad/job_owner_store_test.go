package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/yolo-labz/wa/v2/internal/adapters/secondary/sqlitetokens"
	"github.com/yolo-labz/wa/v2/internal/app"
)

// TestSyncJobs_DirectStoreRevokeAndExpiry: job access revalidates in
// SQLite itself. A revoke through a second store handle (as `wad token
// revoke` does from its own process) and an expiry written straight to
// the file deny the owner on the very next call, the admin override
// returns no result for them, and a failed store read denies.
func TestSyncJobs_DirectStoreRevokeAndExpiry(t *testing.T) {
	ctx := t.Context()
	path := filepath.Join(t.TempDir(), "tokens.db")
	daemon, err := sqlitetokens.Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	cli, err := sqlitetokens.Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = cli.Close() }()
	issue := func(name string, scope sqlitetokens.Scope) app.Principal {
		tok, err := cli.Issue(ctx, name, scope, 0)
		if err != nil {
			t.Fatal(err)
		}
		return app.TokenPrincipal(tok.ID)
	}
	a, b, adm := issue("a", sqlitetokens.ScopeSend), issue("b", sqlitetokens.ScopeSend), issue("adm", sqlitetokens.ScopeAdmin)
	e := app.NewExecutor(app.DefaultExecLimits, app.ExecAuth{Check: daemon.Check, Audit: &recordingAudit{}})
	defer e.Close()
	release := make(chan struct{})
	defer close(release)
	work := func(ctx context.Context) (json.RawMessage, error) {
		select {
		case <-release:
			return json.RawMessage(`{"ok":true}`), nil
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	start := func(p app.Principal) string {
		raw, err := e.Run(ctx, p.ID, "sync.force", true, work)
		var r app.JobView
		if err != nil || json.Unmarshal(raw, &r) != nil {
			t.Fatalf("run: %v %s", err, raw)
		}
		if v, err := e.Get(ctx, p, r.ID); err != nil || v.State == "" {
			t.Fatalf("owner get before revocation: %+v %v", v, err)
		}
		return r.ID
	}
	ja, jb := start(a), start(b)

	if err := cli.Revoke(ctx, a.ID[len("token:"):]); err != nil {
		t.Fatal(err)
	}
	raw, err := sql.Open("sqlite", "file:"+path+"?_pragma=busy_timeout(5000)")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = raw.Close() }()
	if _, err := raw.ExecContext(ctx, `UPDATE tokens SET expires_at = ? WHERE token_id = ?`, time.Now().Add(-time.Minute).Unix(), b.ID[len("token:"):]); err != nil {
		t.Fatal(err)
	}
	for p, id := range map[app.Principal]string{a: ja, b: jb} {
		if _, err := e.Get(ctx, p, id); !errors.Is(err, app.ErrPrincipalInvalid) {
			t.Errorf("%s still served after revoke/expiry: %v", p.ID, err)
		}
		if v, err := e.Get(ctx, adm, id); err != nil || v.Result != nil ||
			(v.State != app.StateCancelRequested && v.State != app.StateCancelled) {
			t.Errorf("override on %s's job: %+v %v, want cancelled with no result", p.ID, v, err)
		}
	}

	if err := daemon.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := e.Get(ctx, adm, ja); !errors.Is(err, app.ErrPrincipalInvalid) {
		t.Errorf("store read failure served: %v", err)
	}
}
