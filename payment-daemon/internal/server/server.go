// Package server wraps the gRPC server lifecycle for the payment-daemon.
//
// The listener is a unix socket at the configured path. The server binds
// the socket on Serve() and removes the file on graceful stop so the next
// run can rebind cleanly.
package server

import (
	"errors"
	"fmt"
	"log/slog"
	"net"
	"os"

	"google.golang.org/grpc"

	pb "github.com/Cloud-SPE/livepeer-network-rewrite/livepeer-network-protocol/proto-go/livepeer/payments/v1"
	"github.com/Cloud-SPE/livepeer-network-rewrite/payment-daemon/internal/service"
)

// ErrStopped is returned by Serve after a graceful shutdown.
var ErrStopped = errors.New("server stopped")

// Server owns the unix socket listener and the gRPC server.
type Server struct {
	socketPath string
	logger     *slog.Logger
	grpcServer *grpc.Server
}

// New constructs a Server that will register svc on the gRPC surface and
// listen at socketPath.
func New(svc *service.Service, socketPath string, logger *slog.Logger) *Server {
	if logger == nil {
		logger = slog.Default()
	}
	gs := grpc.NewServer()
	pb.RegisterPayeeDaemonServer(gs, svc)
	return &Server{
		socketPath: socketPath,
		logger:     logger,
		grpcServer: gs,
	}
}

// Serve binds the unix socket and runs the gRPC server. Blocks until the
// listener errors or GracefulStop is called.
func (s *Server) Serve() error {
	// Remove a stale socket file if a prior run left it behind. This is
	// safe because the bbolt file lock prevents two daemons from running
	// against the same DB; if Serve fails after the unlink, we surface it.
	if err := os.Remove(s.socketPath); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("remove stale socket %s: %w", s.socketPath, err)
	}
	lis, err := net.Listen("unix", s.socketPath)
	if err != nil {
		return fmt.Errorf("listen unix %s: %w", s.socketPath, err)
	}
	if err := os.Chmod(s.socketPath, 0o660); err != nil {
		return fmt.Errorf("chmod socket: %w", err)
	}
	s.logger.Info("gRPC listening", "socket", s.socketPath)
	if err := s.grpcServer.Serve(lis); err != nil {
		return fmt.Errorf("grpc serve: %w", err)
	}
	return ErrStopped
}

// GracefulStop stops the gRPC server and lets in-flight RPCs finish.
func (s *Server) GracefulStop() {
	s.grpcServer.GracefulStop()
	if err := os.Remove(s.socketPath); err != nil && !errors.Is(err, os.ErrNotExist) {
		s.logger.Warn("remove socket on stop", "err", err)
	}
}
