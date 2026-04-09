package socket

import (
	"context"
	"net"
	"os"

	"github.com/creachadair/jrpc2"
	"github.com/creachadair/jrpc2/channel"
)

// peerUIDFunc is the function used to obtain the peer UID from a unix
// connection. It defaults to the platform-specific peerUID implementation.
// Tests can override this to simulate peer-uid mismatches without requiring
// a different OS user.
var peerUIDFunc = peerUID

// acceptLoop runs the listener accept loop. It is started as a goroutine by
// Server.Run and exits when the listener is closed (during shutdown).
func (s *Server) acceptLoop() {
	for {
		conn, err := s.listener.Accept()
		if err != nil {
			// listener.Close() during shutdown causes Accept to return an error.
			// This is the normal exit path.
			select {
			case <-s.ctx.Done():
				return
			default:
				s.log.Error("accept failed", "error", err)
				return
			}
		}

		uc, ok := conn.(*net.UnixConn)
		if !ok {
			s.log.Error("accepted non-unix connection, closing")
			_ = conn.Close()
			continue
		}

		// Peer credential check.
		uid, err := peerUIDFunc(uc)
		if err != nil {
			s.log.Warn("peer credential check failed", "error", err)
			_ = uc.Close()
			continue
		}

		euid := uint32(os.Geteuid())
		if uid != euid {
			s.log.Warn("peer uid mismatch, rejecting connection",
				"expected", euid,
				"actual", uid,
			)
			_ = uc.Close()
			continue
		}

		// Build per-connection state.
		id := s.connCounter.Add(1)
		c := newConnection(id, uid, uc, s.ctx, s.log)
		c.log.Info("connection accepted")

		// Register the connection and start its writer goroutine.
		s.addConn(c)
		c.startWriter()

		s.wg.Add(1)
		go s.serveConn(c)
	}
}

// serveConn runs the jrpc2 server on a single connection. It blocks until the
// connection is closed or the server shuts down.
func (s *Server) serveConn(c *Connection) {
	defer s.wg.Done()
	defer func() {
		s.removeConn(c)
		c.cancel()
		_ = c.raw.Close()
		// Release subscriptions.
		c.mu.Lock()
		c.subscriptions = make(map[string]*Subscription)
		c.mu.Unlock()
	}()

	// Create line-delimited channel on the raw connection.
	ch := channel.Line(c.raw, c.raw)

	// Create a jrpc2 server for this connection.
	assigner := &dispatchAssigner{server: s, conn: c}
	opts := s.serverOptions()

	// Use the connection's context for the jrpc2 server.
	opts.NewContext = func() context.Context {
		return c.ctx
	}

	srv := jrpc2.NewServer(assigner, opts)
	srv.Start(ch)

	c.log.Info("connection serving")

	// Wait for the jrpc2 server to finish (client disconnect or error).
	err := srv.Wait()
	if err != nil {
		c.log.Debug("connection closed", "reason", err)
	} else {
		c.log.Info("connection closed cleanly")
	}
}
