package server

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"

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
	msg, stream, err := workerconn.AcceptQUICRegister(ctx, conn)
	if err != nil {
		_ = conn.CloseWithError(1, err.Error())
		return
	}
	if workerconn.IsAttachRegister(msg) {
		s.handleAttachQUIC(ctx, conn, msg, stream)
		return
	}
	// Legacy backend-ids register: acknowledge on the stream as before.
	if err := json.NewEncoder(stream).Encode(workerconn.TunnelMessage{Type: workerconn.MessageTypeResponse, ID: msg.ID, StatusCode: http.StatusOK}); err != nil {
		_ = stream.Close()
		_ = conn.CloseWithError(1, err.Error())
		return
	}
	_ = stream.Close()
	backendIDs := workerconn.RegisterBackendIDs(msg)
	if len(backendIDs) == 0 {
		_ = conn.CloseWithError(1, "backend ids are required")
		return
	}
	authz := workerconn.RegisterAuthorization(msg)
	if s.authenticateAttachCredential(authz) == nil && !s.adminTokenMatches(authz) {
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
	untrack := s.trackAttachedHost(authz, forwarder)
	defer func() {
		untrack()
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
