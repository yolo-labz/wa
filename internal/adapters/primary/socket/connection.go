package socket

import (
	"context"
	"log/slog"
	"net"
	"sync"
	"sync/atomic"
	"time"
)

// Subscription records a client's opt-in to server-initiated event
// notifications. Only the owning connection can close or unsubscribe it.
type Subscription struct {
	// id is the opaque identifier returned to the client from subscribe (UUID v4).
	id string
	// events is the set of event type names the client opted into.
	events map[string]struct{}
	// createdAt records when the subscription was created.
	createdAt time.Time
}

// Connection is the per-connection state. One *Connection per accepted
// net.Conn. Lifetime: from listener.Accept() return to the per-connection
// goroutine's return.
type Connection struct {
	// id is monotonic, assigned by Server.connCounter.Add(1).
	id uint64
	// peerUID is set by the peer-cred check; zero before the check.
	peerUID uint32
	// raw is the accepted unix socket connection.
	raw *net.UnixConn
	// log is derived from Server.log with conn and peer_uid attrs.
	log *slog.Logger
	// ctx is derived from the server's root context; cancelled when
	// the connection closes.
	ctx context.Context
	// cancel cancels ctx.
	cancel context.CancelFunc
	// subscriptions is keyed by subscription id; guarded by mu.
	subscriptions map[string]*Subscription
	// out is the bounded outbound mailbox for push notifications.
	// Capacity is 1024.
	out chan []byte
	// inFlight tracks the current in-flight request count.
	inFlight atomic.Int32
	// mu guards the subscriptions map.
	mu sync.Mutex
	// createdAt records when the connection was accepted (wall clock).
	createdAt time.Time
}

// newConnection creates a Connection with the given id, peer UID, raw unix
// connection, parent context, and logger. The connection's context is derived
// from parentCtx and cancelled via the returned Connection's cancel func.
// jrpc2 wiring is deferred to a later phase (US1).
func newConnection(id uint64, peerUID uint32, conn *net.UnixConn, parentCtx context.Context, log *slog.Logger) *Connection {
	ctx, cancel := context.WithCancel(parentCtx)
	return &Connection{
		id:            id,
		peerUID:       peerUID,
		raw:           conn,
		log:           log.With("conn", id, "peer_uid", peerUID),
		ctx:           ctx,
		cancel:        cancel,
		subscriptions: make(map[string]*Subscription),
		out:           make(chan []byte, 1024),
		createdAt:     time.Now(),
	}
}
