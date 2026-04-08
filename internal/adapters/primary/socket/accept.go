package socket

import (
	"context"
	"net"
	"os"

	"github.com/creachadair/jrpc2"
	"github.com/creachadair/jrpc2/channel"
)

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
			conn.Close()
			continue
		}

		// Peer credential check.
		uid, err := peerUID(uc)
		if err != nil {
			s.log.Warn("peer credential check failed", "error", err)
			uc.Close()
			continue
		}

		euid := uint32(os.Geteuid())
		if uid != euid {
			s.log.Warn("peer uid mismatch, rejecting connection",
				"expected", euid,
				"actual", uid,
			)
			uc.Close()
			continue
		}

		// Build per-connection state.
		id := s.connCounter.Add(1)
		c := newConnection(id, uid, uc, s.ctx, s.log)
		c.log.Info("connection accepted")

		s.wg.Add(1)
		go s.serveConn(c)
	}
}

// serveConn runs the jrpc2 server on a single connection. It blocks until the
// connection is closed or the server shuts down.
func (s *Server) serveConn(c *Connection) {
	defer s.wg.Done()
	defer c.cancel()
	defer c.raw.Close()

	// Create line-delimited channel on the raw connection.
	ch := channel.Line(c.raw, c.raw)

	// Create a jrpc2 server for this connection.
	assigner := &dispatchAssigner{server: s}
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
