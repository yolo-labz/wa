package sqlitehistory

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"fmt"
	"io"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"modernc.org/sqlite"
)

// faultSQLite wraps ONLY temporary SQLite databases. Failures are injected at
// the driver boundary, leaving the filesystem writable and real triggers active.
type faultSQLite struct {
	mu                sync.Mutex
	code              int
	messageOnly       bool
	blockBegin        bool
	busyRemaining     int
	attempts, upserts int
	fixtureSizes      []int
	queries           []string
	blockID           string
	entered           chan struct{}
	release           chan struct{}
}

type faultCode int

func (e faultCode) Error() string { return fmt.Sprintf("injected SQLite code %d", e) }
func (e faultCode) Code() int     { return int(e) }

func (f *faultSQLite) before(ctx context.Context, q string, args []driver.NamedValue) error {
	f.mu.Lock()
	f.queries = append(f.queries, q)
	insert := strings.HasPrefix(strings.TrimSpace(q), "INSERT INTO messages (")
	if strings.HasPrefix(q, "INSERT INTO health_probe") {
		f.upserts++
	}
	var block bool
	if insert {
		f.attempts++
		if len(args) > 10 && args[0].Value == "wad-health@local" {
			f.fixtureSizes = append(f.fixtureSizes, len(args[10].Value.([]byte)))
		}
		block = len(args) > 2 && args[2].Value == f.blockID && f.entered != nil
	}
	code, messageOnly := f.code, f.messageOnly
	if insert && f.busyRemaining > 0 {
		f.busyRemaining--
		code = 5
	}
	entered, release := f.entered, f.release
	f.mu.Unlock()
	if block {
		select {
		case entered <- struct{}{}:
		default:
		}
		select {
		case <-release:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	if (insert || (!messageOnly && strings.HasPrefix(q, "INSERT INTO health_probe"))) && code != 0 {
		return faultCode(code)
	}
	return nil
}

func (f *faultSQLite) setCode(code int) { f.mu.Lock(); f.code = code; f.mu.Unlock() }

type faultConnector struct {
	dsn   string
	fault *faultSQLite
}

func (c *faultConnector) Connect(context.Context) (driver.Conn, error) {
	conn, err := (&sqlite.Driver{}).Open(c.dsn)
	if err != nil {
		return nil, err
	}
	return &faultConn{Conn: conn, fault: c.fault}, nil
}
func (*faultConnector) Driver() driver.Driver { return &sqlite.Driver{} }

type faultConn struct {
	driver.Conn
	fault *faultSQLite
}

func (c *faultConn) BeginTx(ctx context.Context, opts driver.TxOptions) (driver.Tx, error) {
	c.fault.mu.Lock()
	block, entered, release := c.fault.blockBegin, c.fault.entered, c.fault.release
	c.fault.blockBegin = false
	c.fault.mu.Unlock()
	if block {
		entered <- struct{}{}
		select {
		case <-release:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	return c.Conn.(driver.ConnBeginTx).BeginTx(ctx, opts)
}

func (c *faultConn) Prepare(q string) (driver.Stmt, error) {
	return c.PrepareContext(context.Background(), q)
}

func (c *faultConn) PrepareContext(ctx context.Context, q string) (driver.Stmt, error) {
	stmt, err := c.Conn.(driver.ConnPrepareContext).PrepareContext(ctx, q)
	if err != nil {
		return nil, err
	}
	return &faultStmt{Stmt: stmt, fault: c.fault, query: q}, nil
}

func (c *faultConn) ExecContext(ctx context.Context, q string, args []driver.NamedValue) (driver.Result, error) {
	if err := c.fault.before(ctx, q, args); err != nil {
		return nil, err
	}
	return c.Conn.(driver.ExecerContext).ExecContext(ctx, q, args)
}

func (c *faultConn) QueryContext(ctx context.Context, q string, args []driver.NamedValue) (driver.Rows, error) {
	return c.Conn.(driver.QueryerContext).QueryContext(ctx, q, args)
}

type faultStmt struct {
	driver.Stmt
	fault *faultSQLite
	query string
}

func (s *faultStmt) ExecContext(ctx context.Context, args []driver.NamedValue) (driver.Result, error) {
	if err := s.fault.before(ctx, s.query, args); err != nil {
		return nil, err
	}
	return s.Stmt.(driver.StmtExecContext).ExecContext(ctx, args)
}

func (s *faultStmt) QueryContext(ctx context.Context, args []driver.NamedValue) (driver.Rows, error) {
	return s.Stmt.(driver.StmtQueryContext).QueryContext(ctx, args)
}

func openFaultStore(t *testing.T, path string) (*Store, *faultSQLite) {
	t.Helper()
	if path == "" {
		path = filepath.Join(t.TempDir(), "messages.db")
	}
	s, err := Open(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.db.Close(); err != nil {
		t.Fatal(err)
	}
	f := &faultSQLite{}
	s.db = sql.OpenDB(&faultConnector{dsn: "file:" + path + "?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)&_pragma=trusted_schema(OFF)&_txlock=immediate", fault: f})
	s.db.SetMaxOpenConns(1)
	t.Cleanup(func() {
		if err := s.Close(); err != nil {
			t.Error(err)
		}
	})
	return s, f
}

// Keep interface assertions beside the fake so a driver API change fails loudly.
var (
	_ driver.ConnBeginTx        = (*faultConn)(nil)
	_ driver.ConnPrepareContext = (*faultConn)(nil)
	_ driver.ExecerContext      = (*faultConn)(nil)
	_ driver.QueryerContext     = (*faultConn)(nil)
	_ driver.StmtExecContext    = (*faultStmt)(nil)
	_ driver.StmtQueryContext   = (*faultStmt)(nil)
	_ io.Closer                 = (*Store)(nil)
)
