package server

import (
	"context"
	"log"

	"github.com/Cloud-SPE/livepeer-network-modules/capability-broker/internal/workerconn"
	"github.com/quic-go/quic-go"
)

// The QUIC attach listener.
//
// A member's agent may attach over QUIC instead of WebSocket; the
// document and the tunnel are the same either way (runner-attach §2).
// This listener once also accepted a legacy "backend ids" register that
// bound operator-configured worker:// backends to a connection. That
// grammar is gone, so a register frame that carries no attach document
// is now nothing the broker can serve, and is refused rather than
// silently accepted into a registry nothing reads.

func (s *Server) runAttachQUIC(ctx context.Context, addr string) error {
	tlsConf, err := workerconn.ServerTLSConfig()
	if err != nil {
		return err
	}
	listener, err := quic.ListenAddr(addr, tlsConf, nil)
	if err != nil {
		return err
	}
	defer func() { _ = listener.Close() }()
	log.Printf("listening on %s (attach quic)", addr)
	for {
		conn, err := listener.Accept(ctx)
		if err != nil {
			return err
		}
		go s.handleAttachQUICConn(ctx, conn)
	}
}

func (s *Server) handleAttachQUICConn(ctx context.Context, conn *quic.Conn) {
	msg, stream, err := workerconn.AcceptQUICRegister(ctx, conn)
	if err != nil {
		_ = conn.CloseWithError(1, err.Error())
		return
	}
	if !workerconn.IsAttachRegister(msg) {
		// Naming what is missing rather than closing silently: an
		// agent built against the old grammar would otherwise see a
		// connection that drops for no stated reason.
		_ = stream.Close()
		_ = conn.CloseWithError(1, "a register frame must carry an attach document")
		return
	}
	s.handleAttachQUIC(ctx, conn, msg, stream)
}
