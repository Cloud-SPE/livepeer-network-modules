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

	mu  sync.Mutex
	lis net.Listener
}

type ReceiverAdminConfig struct {
	Token string
}

// NewSender constructs a Server registered with PayerDaemon (sender
// mode). PayeeDaemon RPCs are not mounted; calls to them return
// UNIMPLEMENTED. rec may be nil (no metrics).
func NewSender(svc pb.PayerDaemonServer, socketPath string, rec metrics.Recorder, logger *slog.Logger) *Server {
	return NewSenderWithAdmin(svc, nil, SenderAdminConfig{}, socketPath, rec, logger)
}

// SenderAdminConfig gates the PayerAdmin surface. An empty token leaves
// it mounted but refusing, exactly as the receiver's does: a surface
// that can move the clock the release rule reads should be closed unless
// an operator deliberately opened it.
type SenderAdminConfig struct {
	Token string
}

// NewSenderWithAdmin constructs a sender Server that also mounts
// PayerAdmin. admin may be nil (not mounted).
func NewSenderWithAdmin(svc pb.PayerDaemonServer, admin pb.PayerAdminServer, adminCfg SenderAdminConfig,
	socketPath string, rec metrics.Recorder, logger *slog.Logger) *Server {
	if logger == nil {
		logger = slog.Default()
	}
	gs := grpc.NewServer(grpc.ChainUnaryInterceptor(
		metricsInterceptor(metrics.RoleSender, rec),
		senderAdminAuthInterceptor(adminCfg.Token),
	))
	pb.RegisterPayerDaemonServer(gs, svc)
	if admin != nil {
		pb.RegisterPayerAdminServer(gs, admin)
	}
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

// Listen binds the unix socket without accepting on it. Idempotent.
//
// It exists so a caller can know the socket is there before anything
// dials it. Serve does the bind too, but it does it on the far side of
// the `go Serve()` that callers write, so "the goroutine has started"
// and "the socket exists" are two different moments — and a client that
// dialled in between got ENOENT. That is a race a caller cannot close
// from the outside without polling for a file and hoping.
func (s *Server) Listen() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.lis != nil {
		return nil
	}
	// Remove a stale socket file if a prior run left it behind.
	if err := os.Remove(s.socketPath); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("remove stale socket %s: %w", s.socketPath, err)
	}
	lis, err := net.Listen("unix", s.socketPath)
	if err != nil {
		return fmt.Errorf("listen unix %s: %w", s.socketPath, err)
	}
	if err := os.Chmod(s.socketPath, 0o660); err != nil {
		_ = lis.Close()
		return fmt.Errorf("chmod socket: %w", err)
	}
	s.lis = lis
	s.logger.Info("gRPC listening", "socket", s.socketPath)
	return nil
}

// Serve binds the unix socket (unless Listen already did) and runs the
// gRPC server. Blocks until the listener errors or GracefulStop is
// called.
func (s *Server) Serve() error {
	if err := s.Listen(); err != nil {
		return err
	}
	s.mu.Lock()
	lis := s.lis
	s.mu.Unlock()
	if err := s.grpcServer.Serve(lis); err != nil {
		return fmt.Errorf("grpc serve: %w", err)
	}
	return ErrStopped
}

// GracefulStop stops the gRPC server and lets in-flight RPCs finish.
func (s *Server) GracefulStop() {
	s.grpcServer.GracefulStop()
	// A server that was bound but never served still holds the socket;
	// GracefulStop on the gRPC server does not know about it.
	s.mu.Lock()
	if s.lis != nil {
		_ = s.lis.Close()
		s.lis = nil
	}
	s.mu.Unlock()
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

// senderAdminAuthInterceptor gates PayerAdmin the way its receiver
// counterpart gates PayeeAdmin.
func senderAdminAuthInterceptor(token string) grpc.UnaryServerInterceptor {
	return adminAuthInterceptor("/livepeer.payments.v1.PayerAdmin/", "payer admin token", token)
}

func receiverAdminAuthInterceptor(token string) grpc.UnaryServerInterceptor {
	return adminAuthInterceptor("/livepeer.payments.v1.PayeeAdmin/", "payee admin token", token)
}

// adminAuthInterceptor gates one admin service prefix behind a bearer
// token. Shared by both roles so the two surfaces cannot drift into
// different answers for "no token configured".
func adminAuthInterceptor(prefix, label, token string) grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req interface{}, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (interface{}, error) {
		if !strings.HasPrefix(info.FullMethod, prefix) {
			return handler(ctx, req)
		}
		if strings.TrimSpace(token) == "" {
			return nil, status.Errorf(codes.PermissionDenied, "%s is not configured", label)
		}
		md, _ := metadata.FromIncomingContext(ctx)
		authz := ""
		if vals := md.Get("authorization"); len(vals) > 0 {
			authz = vals[0]
		}
		if authz != "Bearer "+token {
			return nil, status.Errorf(codes.PermissionDenied, "invalid %s", label)
		}
		return handler(ctx, req)
	}
}
