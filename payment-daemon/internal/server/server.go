// Package server wraps the gRPC server lifecycle for the payment-daemon.
//
// The daemon binds a single unix socket and registers EITHER the
// PayerDaemon service (sender mode) OR the PayeeDaemon service
// (receiver mode), per the operator's `--mode` choice. The selection
// happens at boot and stays for the process lifetime.
//
// Both services share boot, signal handling, and the unix-socket
// listener; they differ only in which service interface is mounted.
package server

import (
	"errors"
	"fmt"
	"log/slog"
	"net"
	"os"

	"google.golang.org/grpc"

	pb "github.com/Cloud-SPE/livepeer-network-rewrite/livepeer-network-protocol/proto-go/livepeer/payments/v1"
)

// ErrStopped is returned by Serve after a graceful shutdown.
var ErrStopped = errors.New("server stopped")

// Mode names the gRPC service the daemon exposes for this run.
type Mode string

const (
	ModeSender   Mode = "sender"
	ModeReceiver Mode = "receiver"
)

// Server owns the unix socket listener and the gRPC server.
type Server struct {
	socketPath string
	logger     *slog.Logger
	grpcServer *grpc.Server
}

// NewSender constructs a Server registered with PayerDaemon (sender
// mode). PayeeDaemon RPCs are not mounted; calls to them return
// UNIMPLEMENTED.
func NewSender(svc pb.PayerDaemonServer, socketPath string, logger *slog.Logger) *Server {
	if logger == nil {
		logger = slog.Default()
	}
	gs := grpc.NewServer()
	pb.RegisterPayerDaemonServer(gs, svc)
	return &Server{socketPath: socketPath, logger: logger, grpcServer: gs}
}

// NewReceiver constructs a Server registered with PayeeDaemon (receiver
// mode). PayerDaemon RPCs are not mounted.
func NewReceiver(svc pb.PayeeDaemonServer, socketPath string, logger *slog.Logger) *Server {
	if logger == nil {
		logger = slog.Default()
	}
	gs := grpc.NewServer()
	pb.RegisterPayeeDaemonServer(gs, svc)
	return &Server{socketPath: socketPath, logger: logger, grpcServer: gs}
}

// Serve binds the unix socket and runs the gRPC server. Blocks until
// the listener errors or GracefulStop is called.
func (s *Server) Serve() error {
	// Remove a stale socket file if a prior run left it behind.
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
