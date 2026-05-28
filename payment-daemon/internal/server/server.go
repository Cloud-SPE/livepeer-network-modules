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
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"

	pb "github.com/Cloud-SPE/livepeer-network-modules/livepeer-network-protocol/proto-go/livepeer/payments/v1"
	"github.com/Cloud-SPE/livepeer-network-modules/payment-daemon/internal/providers/metrics"
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

type ReceiverAdminConfig struct {
	Token string
}

// NewSender constructs a Server registered with PayerDaemon (sender
// mode). PayeeDaemon RPCs are not mounted; calls to them return
// UNIMPLEMENTED. rec may be nil (no metrics).
func NewSender(svc pb.PayerDaemonServer, socketPath string, rec metrics.Recorder, logger *slog.Logger) *Server {
	if logger == nil {
		logger = slog.Default()
	}
	gs := grpc.NewServer(grpc.UnaryInterceptor(metricsInterceptor(metrics.RoleSender, rec)))
	pb.RegisterPayerDaemonServer(gs, svc)
	return &Server{socketPath: socketPath, logger: logger, grpcServer: gs}
}

// NewReceiver constructs a Server registered with PayeeDaemon (receiver
// mode). PayerDaemon RPCs are not mounted. rec may be nil (no metrics).
func NewReceiver(svc pb.PayeeDaemonServer, admin pb.PayeeAdminServer, adminCfg ReceiverAdminConfig, socketPath string, rec metrics.Recorder, logger *slog.Logger) *Server {
	if logger == nil {
		logger = slog.Default()
	}
	gs := grpc.NewServer(grpc.ChainUnaryInterceptor(
		metricsInterceptor(metrics.RoleReceiver, rec),
		receiverAdminAuthInterceptor(adminCfg.Token),
	))
	pb.RegisterPayeeDaemonServer(gs, svc)
	if admin != nil {
		pb.RegisterPayeeAdminServer(gs, admin)
	}
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

// metricsInterceptor records per-RPC count, latency, and the in-flight
// gauge for the given role. A nil Recorder yields a passthrough.
func metricsInterceptor(role string, rec metrics.Recorder) grpc.UnaryServerInterceptor {
	if rec == nil {
		return func(ctx context.Context, req any, _ *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
			return handler(ctx, req)
		}
	}
	table := newInFlightTable()
	return func(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
		method := shortMethod(info.FullMethod)
		c := table.get(info.FullMethod)
		rec.SetGRPCInFlight(role, method, int(c.Add(1)))
		start := time.Now()
		resp, err := handler(ctx, req)
		rec.ObserveGRPC(role, method, time.Since(start))
		rec.IncGRPCRequest(role, method, status.Code(err).String())
		rec.SetGRPCInFlight(role, method, int(c.Add(-1)))
		return resp, err
	}
}

// shortMethod turns "/livepeer.payments.v1.PayeeDaemon/ProcessPayment"
// into "ProcessPayment".
func shortMethod(full string) string {
	if i := strings.LastIndex(full, "/"); i >= 0 {
		return full[i+1:]
	}
	return full
}

// inFlightTable tracks per-FullMethod live request counts so the
// interceptor can set the in-flight gauge without unbounded growth.
type inFlightTable struct {
	mu     sync.Mutex
	counts map[string]*atomic.Int64
}

func newInFlightTable() *inFlightTable { return &inFlightTable{counts: map[string]*atomic.Int64{}} }

func (t *inFlightTable) get(key string) *atomic.Int64 {
	t.mu.Lock()
	c, ok := t.counts[key]
	if !ok {
		c = &atomic.Int64{}
		t.counts[key] = c
	}
	t.mu.Unlock()
	return c
}

func receiverAdminAuthInterceptor(token string) grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req interface{}, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (interface{}, error) {
		if !strings.HasPrefix(info.FullMethod, "/livepeer.payments.v1.PayeeAdmin/") {
			return handler(ctx, req)
		}
		if strings.TrimSpace(token) == "" {
			return nil, status.Error(codes.PermissionDenied, "payee admin token is not configured")
		}
		md, _ := metadata.FromIncomingContext(ctx)
		authz := ""
		if vals := md.Get("authorization"); len(vals) > 0 {
			authz = vals[0]
		}
		want := "Bearer " + token
		if authz != want {
			return nil, status.Error(codes.PermissionDenied, "invalid payee admin token")
		}
		return handler(ctx, req)
	}
}
