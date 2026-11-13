package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/yolo-labz/wa/v2/internal/adapters/primary/admin"
	"github.com/yolo-labz/wa/v2/internal/adapters/secondary/slogaudit"
	"github.com/yolo-labz/wa/v2/internal/adapters/secondary/sqlitetokens"
	"github.com/yolo-labz/wa/v2/internal/app"
)

// adminTokenShim adapts *sqlitetokens.Store → admin.TokenStore.
type adminTokenShim struct{ store *sqlitetokens.Store }

func toAdminToken(t sqlitetokens.Token) admin.Token {
	return admin.Token{
		ID: t.ID, Name: t.Name, Scope: string(t.Scope), Raw: t.Raw,
		Created: t.CreatedAt, Expires: t.ExpiresAt, LastUsed: t.LastUsedAt, RevokedAt: t.RevokedAt,
	}
}

func (s adminTokenShim) Verify(ctx context.Context, raw string) (admin.Token, error) {
	t, err := s.store.Verify(ctx, raw)
	return toAdminToken(t), err
}

func (s adminTokenShim) Lookup(ctx context.Context, id string) (admin.Token, error) {
	t, err := s.store.Lookup(ctx, id)
	if errors.Is(err, sqlitetokens.ErrTokenNotFound) || errors.Is(err, sqlitetokens.ErrTokenRevoked) || errors.Is(err, sqlitetokens.ErrTokenExpired) {
		err = fmt.Errorf("%w: %w", admin.ErrTokenGone, err)
	}
	return toAdminToken(t), err
}

func (s adminTokenShim) List(ctx context.Context) ([]admin.Token, error) {
	list, err := s.store.List(ctx)
	out := make([]admin.Token, 0, len(list))
	for _, t := range list {
		out = append(out, toAdminToken(t))
	}
	return out, err
}

func (s adminTokenShim) Issue(ctx context.Context, name, scope string, ttl time.Duration) (admin.Token, error) {
	t, err := s.store.Issue(ctx, name, sqlitetokens.Scope(scope), ttl)
	return toAdminToken(t), err
}

func (s adminTokenShim) Revoke(ctx context.Context, id string) error { return s.store.Revoke(ctx, id) }

// adminAuditShim adapts *slogaudit.Audit → admin.AuditTail.
type adminAuditShim struct{ a *slogaudit.Audit }

func (s adminAuditShim) Tail(n int) ([]admin.AuditRecord, error) {
	es, err := s.a.Tail(n)
	out := make([]admin.AuditRecord, 0, len(es))
	for _, e := range es {
		out = append(out, admin.AuditRecord{
			Time: e.EventTime, Actor: e.Actor, Action: e.Action, Subject: e.Subject,
			Decision: e.Decision, Detail: e.Detail, Source: e.Source,
		})
	}
	return out, err
}

func (s adminAuditShim) Verify() error { _, err := s.a.Verify(); return err }

// adminStatus feeds the status page from the dispatcher's one health
// snapshot: the same truth table and reasons /readyz serves.
func adminStatus(d *app.Dispatcher) admin.StatusSource {
	return func(ctx context.Context) (admin.Status, error) {
		raw, unready, err := d.StatusView(ctx)
		var st admin.Status
		if err == nil {
			err = json.Unmarshal(raw, &st)
		}
		st.Unready = unready
		return st, err
	}
}

// startAdminHTTP starts the spec 116 admin listener when WAD_ADMIN_ADDR
// is set. Every refusal is logged and the daemon continues without the
// admin listener; REST and health keep running (SR-116-02). It needs the
// scoped token DB: single-token auth has no scopes (SR-116-01). Returns
// the bound address (nil when not listening) and a shutdown func. pair
// (nil: no pairing routes) is told of each PairSuccess on events.
func startAdminHTTP(ctx context.Context, profile string, audit app.AuditLog, rpc admin.Dispatcher, status admin.StatusSource, pair admin.Pairer, events eventsPumpSource, log *slog.Logger) (net.Addr, func(context.Context) error) {
	noop := func(context.Context) error { return nil }
	addr := os.Getenv("WAD_ADMIN_ADDR")
	if addr == "" {
		return nil, noop
	}
	err := admin.ValidateAddr(addr, os.Getenv("WAD_REST_HTTP_ADDR"), os.Getenv("WAD_HEALTH_HTTP_ADDR"))
	dbPath := os.Getenv("WAD_REST_TOKEN_DB")
	if err == nil && dbPath == "" {
		err = errors.New("WAD_ADMIN_ADDR needs WAD_REST_TOKEN_DB: single-token auth has no scopes, so it gets no admin UI")
	}
	var store *sqlitetokens.Store
	if err == nil {
		store, err = sqlitetokens.Open(ctx, dbPath)
	}
	var ln net.Listener
	if err == nil {
		var lc net.ListenConfig
		if ln, err = lc.Listen(ctx, "tcp", addr); err != nil {
			_ = store.Close()
		}
	}
	if err != nil {
		log.Error("admin http refused; continuing without the admin listener", "err", err)
		return nil, noop
	}
	var origins []string
	for o := range strings.SplitSeq(os.Getenv("WAD_ADMIN_HTTPS_ORIGINS"), ",") {
		if o = strings.TrimSpace(o); o != "" {
			origins = append(origins, o)
		}
	}
	cfg := admin.Config{Profile: profile, HTTPSOrigins: origins, Tokens: adminTokenShim{store}, Audit: audit, RPC: rpc, Pairer: pair, Status: status, Log: log}
	if file, ok := audit.(*slogaudit.Audit); ok {
		cfg.AuditTail = adminAuditShim{file}
	}
	srv := admin.New(cfg)
	unwatch := func() {}
	if pair != nil && events != nil {
		ch, cancel := events.SubscribeStream([]string{"pairing"}, 4)
		wctx, stop := context.WithCancel(ctx)
		go watchPairSuccess(wctx, ch, srv.PairSucceeded)
		unwatch = func() { stop(); cancel() }
	}
	srv.Start(ln)
	log.Info("admin http listening", "addr", ln.Addr().String())
	return ln.Addr(), func(ctx context.Context) error {
		unwatch()
		return errors.Join(srv.Shutdown(ctx), store.Close())
	}
}

const adminTunnelUsage = `usage:
  wad admin-tunnel                                   (inside the container) copy stdin/stdout to WAD_ADMIN_ADDR
  wad admin-tunnel --listen 127.0.0.1:PORT -- CMD…   (operator side) per connection, run CMD with the connection as stdio,
                                                     e.g. -- ssh HOST docker exec -i CONTAINER wad admin-tunnel
The tunnel takes no token: sign in through the browser`

// handleAdminTunnelCommand runs `wad admin-tunnel` (SR-116-02 reach path
// 2). The runtime image has no shell or socat, so wad itself bridges.
func handleAdminTunnelCommand() bool {
	if len(os.Args) < 2 || os.Args[1] != "admin-tunnel" {
		return false
	}
	ctx := context.Background()
	var err error
	switch args := os.Args[2:]; {
	case len(args) == 0:
		err = adminTunnel(ctx, os.Getenv("WAD_ADMIN_ADDR"), os.Stdin, os.Stdout)
	case len(args) >= 4 && args[0] == "--listen" && args[2] == "--":
		var ln net.Listener
		if ln, err = listenForwarder(ctx, args[1]); err == nil {
			err = serveForwarder(ctx, ln, args[3:])
		}
	default:
		err = errors.New(adminTunnelUsage)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "wad admin-tunnel:", err)
		os.Exit(1)
	}
	return true
}

// adminTunnel copies in → WAD_ADMIN_ADDR → out. The address passes the
// same loopback validation as the listener, so the tunnel can never be
// pointed at another host.
func adminTunnel(ctx context.Context, addr string, in io.Reader, out io.Writer) error {
	if err := admin.ValidateAddr(addr); err != nil {
		return err
	}
	var d net.Dialer
	conn, err := d.DialContext(ctx, "tcp", addr)
	if err != nil {
		return err
	}
	defer func() { _ = conn.Close() }()
	sent := make(chan struct{})
	go func() {
		defer close(sent)
		_, _ = io.Copy(conn, in)
		_ = conn.(*net.TCPConn).CloseWrite()
	}()
	_, err = io.Copy(out, conn)
	_ = conn.Close()
	<-sent
	return err
}

// listenForwarder binds the operator-side forwarder to 127.0.0.1 only,
// never a wildcard, hostname or other interface (T17).
func listenForwarder(ctx context.Context, addr string) (net.Listener, error) {
	host, _, err := net.SplitHostPort(addr)
	if ip := net.ParseIP(host); err != nil || ip == nil || !ip.Equal(net.IPv4(127, 0, 0, 1)) {
		return nil, fmt.Errorf("--listen %q: the forwarder binds 127.0.0.1 only", addr)
	}
	var lc net.ListenConfig
	return lc.Listen(ctx, "tcp4", addr)
}

// maxTunnelConns bounds concurrent forwarder children.
const maxTunnelConns = 8

// serveForwarder runs argv once per accepted connection with the
// connection as its stdin/stdout, until ln closes.
//
// At most maxTunnelConns run at once and the rest are closed unanswered,
// so a local process (or a web page opening 127.0.0.1 connections) cannot
// make the operator's machine spawn processes without bound.
func serveForwarder(ctx context.Context, ln net.Listener, argv []string) error {
	slots := make(chan struct{}, maxTunnelConns)
	for {
		conn, err := ln.Accept()
		if err != nil {
			return err
		}
		select {
		case slots <- struct{}{}:
		default:
			_ = conn.Close()
			continue
		}
		go func() {
			defer func() { <-slots; _ = conn.Close() }()
			cmd := exec.CommandContext(ctx, argv[0], argv[1:]...) // #nosec G204 G702 -- the operator's own forwarder command, from their own argv
			cmd.Stdin, cmd.Stdout, cmd.Stderr = conn, conn, os.Stderr
			cmd.WaitDelay = time.Second
			_ = cmd.Run()
		}()
	}
}
