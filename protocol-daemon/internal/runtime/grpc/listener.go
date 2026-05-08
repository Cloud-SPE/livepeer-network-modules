package grpc

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"runtime/debug"
	"strings"
	"sync"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/keepalive"
	"google.golang.org/grpc/reflection"
	"google.golang.org/grpc/status"

	"github.com/Cloud-SPE/livepeer-network-rewrite/chain-commons/providers/logger"
	protocolv1 "github.com/Cloud-SPE/livepeer-network-rewrite/proto-contracts/livepeer/protocol/v1"
)

// ListenerConfig captures the inputs to NewListener.
type ListenerConfig struct {
	SocketPath     string
	Server         *Server
	Logger         logger.Logger
	MaxRecvMsgSize int           // bytes; default 4 MiB
	RPCTimeout     time.Duration // per-call deadline; default 30s
	Version        string        // build version; injected into every log line
}

// Listener owns the unix socket and the *grpc.Server. Construction is
// fallible (socket creation, permissions). Shutdown is graceful: stop
// accepting new RPCs, drain in-flight, close, remove the socket file.
type Listener struct {
	cfg      ListenerConfig
	gsrv     *grpc.Server
	netLn    net.Listener
	healthSv *healthService
	once     sync.Once
}

// NewListener builds the gRPC server, registers services, but does NOT
// yet bind to the socket. Call Serve to start.
func NewListener(cfg ListenerConfig) (*Listener, error) {
	if cfg.Server == nil {
		return nil, errors.New("grpc listener: nil Server")
	}
	if cfg.SocketPath == "" {
		return nil, errors.New("grpc listener: empty socket path")
	}
	if cfg.Logger == nil {
		return nil, errors.New("grpc listener: nil Logger")
	}
	if cfg.MaxRecvMsgSize <= 0 {
		cfg.MaxRecvMsgSize = 4 << 20
	}
	if cfg.RPCTimeout <= 0 {
		cfg.RPCTimeout = 30 * time.Second
	}

	gsrv := grpc.NewServer(
		grpc.MaxRecvMsgSize(cfg.MaxRecvMsgSize),
		grpc.UnaryInterceptor(chainInterceptors(
			recoverInterceptor(cfg.Logger),
			deadlineInterceptor(cfg.RPCTimeout),
			loggingInterceptor(cfg.Logger, cfg.Version),
		)),
		grpc.KeepaliveEnforcementPolicy(keepalive.EnforcementPolicy{
			MinTime:             10 * time.Second,
			PermitWithoutStream: true,
		}),
	)

	protocolv1.RegisterProtocolDaemonServer(gsrv, newAdapter(cfg.Server))

	hs := newHealthService()
	healthpb.RegisterHealthServer(gsrv, hs)
	reflection.Register(gsrv)

	return &Listener{cfg: cfg, gsrv: gsrv, healthSv: hs}, nil
}

// Serve blocks: it cleans a stale socket file (if it's actually a
// socket — never a regular file we don't own), binds the unix socket
// with mode 0o600, marks the health service serving, and serves until
// ctx is cancelled or Stop is called.
func (l *Listener) Serve(ctx context.Context) error {
	if err := prepSocketPath(l.cfg.SocketPath); err != nil {
		return err
	}
	ln, err := net.Listen("unix", l.cfg.SocketPath)
	if err != nil {
		return fmt.Errorf("grpc listener: bind %s: %w", l.cfg.SocketPath, err)
	}
	if err := os.Chmod(l.cfg.SocketPath, 0o600); err != nil {
		_ = ln.Close()
		return fmt.Errorf("grpc listener: chmod socket: %w", err)
	}
	l.netLn = ln
	l.healthSv.SetServing(true)
	l.cfg.Logger.Info("grpc.listening",
		logger.String("socket", l.cfg.SocketPath),
		logger.String("version", l.cfg.Version),
	)

	stopErr := make(chan error, 1)
	stopped := make(chan struct{})
	go func() {
		<-ctx.Done()
		l.Stop()
		close(stopped)
	}()
	go func() {
		stopErr <- l.gsrv.Serve(ln)
	}()

	// Wait for either Serve to return naturally OR for our Stop sequence
	// to complete. Stop completion is the ground truth: once Stop has
	// fully run, the daemon should exit even if a handler goroutine is
	// still blocked.
	select {
	case err = <-stopErr:
	case <-stopped:
		select {
		case err = <-stopErr:
		case <-time.After(100 * time.Millisecond):
			err = grpc.ErrServerStopped
		}
	}

	if err != nil && !errors.Is(err, grpc.ErrServerStopped) {
		return err
	}
	return nil
}

// Stop performs a graceful shutdown. Idempotent. Caps GracefulStop at
// 2s — connection-level keepalive pings can keep grpc-go's GracefulStop
// blocked after a client closes; rather than wedge the daemon, we let
// the process exit reap any leaked goroutines.
func (l *Listener) Stop() {
	l.once.Do(func() {
		l.cfg.Logger.Debug("grpc.stop.begin")
		if l.healthSv != nil {
			l.healthSv.SetServing(false)
		}
		if l.gsrv != nil {
			gracefulDone := make(chan struct{})
			go func() {
				l.gsrv.GracefulStop()
				close(gracefulDone)
			}()
			select {
			case <-gracefulDone:
				l.cfg.Logger.Debug("grpc.stop.graceful_complete")
			case <-time.After(2 * time.Second):
				l.cfg.Logger.Warn("grpc.stop.graceful_timeout")
			}
		}
		if l.netLn != nil {
			_ = l.netLn.Close()
		}
		_ = os.Remove(l.cfg.SocketPath)
		l.cfg.Logger.Info("grpc.stopped", logger.String("socket", l.cfg.SocketPath))
	})
}

// Addr returns the listener address (or empty string before Serve).
func (l *Listener) Addr() string {
	if l.netLn == nil {
		return ""
	}
	return l.netLn.Addr().String()
}

// prepSocketPath ensures the parent directory exists and removes any
// pre-existing socket file at the path. It refuses to remove anything
// that's not a socket — don't blast a regular file the operator might
// have at the same path.
func prepSocketPath(path string) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil { //nolint:gosec // G301: socket dir needs world-traversable so non-root consumers can reach the unix socket
		return fmt.Errorf("grpc listener: mkdir %s: %w", dir, err)
	}
	info, err := os.Lstat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("grpc listener: stat socket: %w", err)
	}
	if info.Mode()&os.ModeSocket == 0 {
		return fmt.Errorf("grpc listener: refusing to remove non-socket file at %s", path)
	}
	if err := os.Remove(path); err != nil {
		return fmt.Errorf("grpc listener: remove stale socket: %w", err)
	}
	return nil
}

// ----- Interceptors -----

// chainInterceptors composes multiple unary interceptors into one. Order
// is outer-first: the leftmost interceptor runs around all others.
func chainInterceptors(ints ...grpc.UnaryServerInterceptor) grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
		chain := handler
		for i := len(ints) - 1; i >= 0; i-- {
			next, intc := chain, ints[i]
			chain = func(ctx context.Context, req any) (any, error) {
				return intc(ctx, req, info, next)
			}
		}
		return chain(ctx, req)
	}
}

func recoverInterceptor(log logger.Logger) grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (out any, err error) {
		defer func() {
			if r := recover(); r != nil {
				log.Error("grpc.handler_panic",
					logger.String("method", info.FullMethod),
					logger.Any("panic", fmt.Sprintf("%v", r)),
					logger.Any("stack", string(debug.Stack())),
				)
				err = status.Errorf(codes.Internal, "internal: handler panic")
			}
		}()
		return handler(ctx, req)
	}
}

func deadlineInterceptor(d time.Duration) grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req any, _ *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
		if _, ok := ctx.Deadline(); !ok {
			var cancel context.CancelFunc
			ctx, cancel = context.WithTimeout(ctx, d)
			defer cancel()
		}
		return handler(ctx, req)
	}
}

func loggingInterceptor(log logger.Logger, version string) grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
		start := time.Now()
		resp, err := handler(ctx, req)
		dur := time.Since(start)
		st := status.Code(err)
		method := info.FullMethod

		fields := []logger.Field{
			logger.String("method", method),
			logger.String("code", st.String()),
			logger.Int("duration_ms", int(dur.Milliseconds())),
			logger.String("version", version),
		}

		switch {
		case strings.Contains(method, "grpc.health.v1.Health/Check"):
			log.Debug("rpc", fields...)
		case err == nil:
			log.Info("rpc", fields...)
		case st == codes.NotFound, st == codes.InvalidArgument, st == codes.Unimplemented:
			log.Warn("rpc", fields...)
		default:
			log.Error("rpc", fields...)
		}
		return resp, err
	}
}

// ----- Standard health service -----

// healthService implements grpc.health.v1.Health with a single overall
// state. We don't surface per-service health because all RPCs share the
// daemon's lifecycle.
type healthService struct {
	healthpb.UnimplementedHealthServer
	mu       sync.RWMutex
	serving  bool
	watchers []chan healthpb.HealthCheckResponse_ServingStatus
}

func newHealthService() *healthService { return &healthService{serving: false} }

func (h *healthService) SetServing(s bool) {
	h.mu.Lock()
	h.serving = s
	state := healthpb.HealthCheckResponse_SERVING
	if !s {
		state = healthpb.HealthCheckResponse_NOT_SERVING
	}
	for _, c := range h.watchers {
		select {
		case c <- state:
		default:
		}
	}
	h.mu.Unlock()
}

func (h *healthService) Check(_ context.Context, _ *healthpb.HealthCheckRequest) (*healthpb.HealthCheckResponse, error) {
	h.mu.RLock()
	defer h.mu.RUnlock()
	if h.serving {
		return &healthpb.HealthCheckResponse{Status: healthpb.HealthCheckResponse_SERVING}, nil
	}
	return &healthpb.HealthCheckResponse{Status: healthpb.HealthCheckResponse_NOT_SERVING}, nil
}

func (h *healthService) Watch(_ *healthpb.HealthCheckRequest, w healthpb.Health_WatchServer) error {
	ch := make(chan healthpb.HealthCheckResponse_ServingStatus, 4)
	h.mu.Lock()
	h.watchers = append(h.watchers, ch)
	initial := healthpb.HealthCheckResponse_NOT_SERVING
	if h.serving {
		initial = healthpb.HealthCheckResponse_SERVING
	}
	h.mu.Unlock()
	if err := w.Send(&healthpb.HealthCheckResponse{Status: initial}); err != nil {
		return err
	}
	for {
		select {
		case s := <-ch:
			if err := w.Send(&healthpb.HealthCheckResponse{Status: s}); err != nil {
				return err
			}
		case <-w.Context().Done():
			return nil
		}
	}
}
