package server

import (
	"context"
	"fmt"
	"log"

	"github.com/Cloud-SPE/livepeer-network-modules/capability-broker/internal/workerconn"
	"github.com/quic-go/quic-go"
)

func (s *Server) runWorkerQUIC(ctx context.Context, addr string) error {
	tlsConf, err := workerconn.ServerTLSConfig()
	if err != nil {
		return err
	}
	listener, err := quic.ListenAddr(addr, tlsConf, nil)
	if err != nil {
		return err
	}
	defer func() { _ = listener.Close() }()
	log.Printf("listening on %s (worker quic)", addr)
	for {
		conn, err := listener.Accept(ctx)
		if err != nil {
			return err
		}
		go s.handleWorkerQUICConn(ctx, conn)
	}
}

func (s *Server) handleWorkerQUICConn(ctx context.Context, conn *quic.Conn) {
	msg, err := workerconn.ReadQUICRegister(ctx, conn)
	if err != nil {
		_ = conn.CloseWithError(1, err.Error())
		return
	}
	backendIDs := workerconn.RegisterBackendIDs(msg)
	if len(backendIDs) == 0 {
		_ = conn.CloseWithError(1, "backend ids are required")
		return
	}
	authz := workerconn.RegisterAuthorization(msg)
	if !s.workerCredentialAllowed(s.currentConfig(), backendIDs, authz) {
		_ = conn.CloseWithError(1, "unauthorized")
		return
	}
	forwarder := workerconn.NewQUICSessionForwarder(conn)
	for _, id := range backendIDs {
		if err := s.workerRegistry.Register(id, forwarder); err != nil {
			_ = conn.CloseWithError(1, fmt.Sprintf("register backend %s: %v", id, err))
			return
		}
	}
	defer func() {
		for _, id := range backendIDs {
			s.workerRegistry.Unregister(id)
		}
		_ = forwarder.Close()
	}()
	select {
	case <-ctx.Done():
	case <-forwarder.Done():
	}
}
