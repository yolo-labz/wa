package socket

import (
	"context"
	"encoding/json"
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
func newConnection(id uint64, peerUID uint32, conn *net.UnixConn, _ context.Context, log *slog.Logger) *Connection {
	// Connection context is independent of the server context so that
	// in-flight requests can drain during graceful shutdown. The connection
	// context is cancelled explicitly by serveConn cleanup or by
	// cancelAllConns when the drain deadline expires.
	ctx, cancel := context.WithCancel(context.Background())
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

// startWriter launches a goroutine that reads frames from c.out and writes
// them to c.raw as newline-delimited lines. On write error, the connection
// context is cancelled. The goroutine exits when c.out is closed or c.ctx
// is cancelled.
func (c *Connection) startWriter() {
	go func() {
		for {
			select {
			case frame, ok := <-c.out:
				if !ok {
					return // channel closed
				}
				// Append newline for line-delimited framing.
				frame = append(frame, '\n')
				if _, err := c.raw.Write(frame); err != nil {
					c.log.Debug("writer: write error, cancelling connection", "error", err)
					c.cancel()
					return
				}
			case <-c.ctx.Done():
				return
			}
		}
	}()
}

// pushNotification attempts a non-blocking send of frame into the outbound
// mailbox. If the mailbox is full, it writes a final -32001 Backpressure
// error frame and closes the connection. If the connection is already
// cancelled, it returns ErrBackpressure without attempting the send.
func (c *Connection) pushNotification(frame []byte) error {
	// Check if the connection is already cancelled.
	select {
	case <-c.ctx.Done():
		return ErrBackpressure
	default:
	}

	select {
	case c.out <- frame:
		return nil
	case <-c.ctx.Done():
		return ErrBackpressure
	default:
		// Backpressure: mailbox full.
		c.log.Warn("outbound mailbox full, closing connection (backpressure)")
		// Best-effort write of the backpressure error frame before closing.
		bp := backpressureFrame()
		bp = append(bp, '\n')
		_, _ = c.raw.Write(bp)
		c.cancel()
		return ErrBackpressure
	}
}

// backpressureFrame returns a JSON-RPC error notification for backpressure.
func backpressureFrame() []byte {
	frame := map[string]any{
		"jsonrpc": "2.0",
		"error": map[string]any{
			"code":    int(CodeBackpressure),
			"message": errCodeName[CodeBackpressure],
		},
	}
	data, _ := json.Marshal(frame)
	return data
}
