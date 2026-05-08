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
	"sync/atomic"
	"time"

	registryv1 "github.com/Cloud-SPE/livepeer-network-rewrite/proto-contracts/livepeer/registry/v1"
	"github.com/Cloud-SPE/livepeer-network-rewrite/service-registry-daemon/internal/providers/logger"
	"github.com/Cloud-SPE/livepeer-network-rewrite/service-registry-daemon/internal/providers/metrics"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/keepalive"
	"google.golang.org/grpc/reflection"
	"google.golang.org/grpc/status"
)

// ListenerConfig captures the inputs to NewListener.
type ListenerConfig struct {
	SocketPath     string
	Server         *Server
	Logger         logger.Logger
	Recorder       metrics.Recorder // nil → metrics.NewNoop()
	MaxRecvMsgSize int              // bytes; default 4 MiB
	RPCTimeout     time.Duration    // per-call deadline; default 30s
	Version        string           // build version; injected into every log line
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
		cfg.Logger = logger.Discard()
	}
	if cfg.Recorder == nil {
		cfg.Recorder = metrics.NewNoop()
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
			metricsInterceptor(cfg.Recorder),
			loggingInterceptor(cfg.Logger, cfg.Version),
		)),
		grpc.KeepaliveEnforcementPolicy(keepalive.EnforcementPolicy{
			MinTime:             10 * time.Second,
			PermitWithoutStream: true,
		}),
	)

	// Register adapters by mode.
	if cfg.Server.HasResolver() {
		registryv1.RegisterResolverServer(gsrv, newResolverAdapter(cfg.Server))
	}
	if cfg.Server.HasPublisher() {
		registryv1.RegisterPublisherServer(gsrv, newPublisherAdapter(cfg.Server))
	}

	// Standard health + reflection — both are operationally critical:
	// k8s probes use gRPC health, and operators use grpcurl to debug.
	hs := newHealthService()
	healthpb.RegisterHealthServer(gsrv, hs)
	reflection.Register(gsrv)

	return &Listener{cfg: cfg, gsrv: gsrv, healthSv: hs}, nil
}

// Serve blocks: it cleans a stale socket file (if it's actually a
// socket — never a regular file we don't own), binds the unix socket
// with mode 0o600, sets liveness on the health service, and serves
// until ctx is cancelled or Stop is called.
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
	l.cfg.Logger.Info("grpc listening", "socket", l.cfg.SocketPath, "version", l.cfg.Version)

	// Tie context cancellation to Stop.
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

	// Wait for either the gRPC Serve loop to return naturally OR for
	// our Stop sequence to complete. The Stop completion is the ground
	// truth: once Stop has fully run (graceful or abandoned), we know
	// the daemon should exit even if a handler goroutine is still
	// blocked. Returning here lets lifecycle proceed to close other
	// resources.
	select {
	case err = <-stopErr:
	case <-stopped:
		// Drain stopErr if it lands shortly after; ignore otherwise.
		select {
		case err = <-stopErr:
		case <-time.After(100 * time.Millisecond):
			err = grpc.ErrServerStopped
		}
	}

	// grpc.Server.Serve returns ErrServerStopped after Stop / GracefulStop;
	// translate to nil for callers.
	if err != nil && !errors.Is(err, grpc.ErrServerStopped) {
		return err
	}
	return nil
}

// Stop performs a graceful shutdown. Idempotent. Uses GracefulStop
// with a fallback to hard Stop if it doesn't drain within the
// shutdown timeout (default 2s) — a misbehaving client should never
// be able to wedge the daemon forever.
func (l *Listener) Stop() {
	l.once.Do(func() {
		l.cfg.Logger.Debug("grpc stop: begin")
		if l.healthSv != nil {
			l.healthSv.SetServing(false)
		}
		if l.gsrv != nil {
			// GracefulStop closes the listener (so Serve returns
			// ErrServerStopped), then waits for in-flight handlers to
			// complete. The per-RPC deadline interceptor caps individual
			// handler latency, so handlers can't wedge GracefulStop.
			// Connection-level keepalive pings have, however, been
			// observed to keep GracefulStop blocked in grpc-go 1.80
			// after a client closed; we cap the wait at 2s and let the
			// binary exit. The process exit reaps any leaked goroutines.
			//
			// We deliberately do NOT call gsrv.Stop() concurrently:
			// grpc-go 1.80's Stop and GracefulStop contend on the same
			// internal mutex and concurrent calls can deadlock.
			gracefulDone := make(chan struct{})
			go func() {
				l.gsrv.GracefulStop()
				close(gracefulDone)
			}()
			select {
			case <-gracefulDone:
				l.cfg.Logger.Debug("grpc stop: graceful complete")
			case <-time.After(2 * time.Second):
				l.cfg.Logger.Warn("graceful stop timed out, abandoning drain")
			}
		}
		if l.netLn != nil {
			_ = l.netLn.Close()
		}
		// Remove the socket file. Safe because we created it; if the
		// path doesn't exist it's a no-op.
		_ = os.Remove(l.cfg.SocketPath)
		l.cfg.Logger.Info("grpc stopped", "socket", l.cfg.SocketPath)
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
// that's not a socket (don't blast a regular file the operator might
// have at the same path).
func prepSocketPath(path string) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil { //nolint:gosec // G301: socket dir needs world-traversable so non-root consumers can reach the unix socket the daemon serves
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
		// Build a function chain bottom-up.
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

// recoverInterceptor catches panics and converts them to gRPC Internal
// errors so a single bad RPC doesn't take the daemon down. Stacks land
// in the logger at error level.
func recoverInterceptor(log logger.Logger) grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (out any, err error) {
		defer func() {
			if r := recover(); r != nil {
				log.Error("grpc handler panic",
					"method", info.FullMethod,
					"panic", fmt.Sprintf("%v", r),
					"stack", string(debug.Stack()),
				)
				err = status.Errorf(codes.Internal, "internal: handler panic")
			}
		}()
		return handler(ctx, req)
	}
}

// deadlineInterceptor enforces a per-RPC default deadline if the caller
// didn't set one. Operators can override via per-call deadlines.
func deadlineInterceptor(d time.Duration) grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
		if _, ok := ctx.Deadline(); !ok {
			var cancel context.CancelFunc
			ctx, cancel = context.WithTimeout(ctx, d)
			defer cancel()
		}
		return handler(ctx, req)
	}
}

// inFlight tracks per-(service,method) live request counts so the
// metricsInterceptor can SetGRPCInFlight without growing unbounded.
// Keyed by FullMethod string ("/svc/method").
type inFlightTable struct {
	mu     sync.Mutex
	counts map[string]*atomic.Int64
}

func newInFlightTable() *inFlightTable {
	return &inFlightTable{counts: map[string]*atomic.Int64{}}
}

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

// metricsInterceptor records per-RPC counters, latencies, and the
// in-flight gauge. It runs INSIDE the deadline interceptor so the
// duration histogram reflects what the handler actually saw, and
// OUTSIDE the logging interceptor so the logger's emitted lines see
// the handler's return.
func metricsInterceptor(rec metrics.Recorder) grpc.UnaryServerInterceptor {
	table := newInFlightTable()
	return func(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
		service, method := splitFullMethod(info.FullMethod)
		key := info.FullMethod
		c := table.get(key)
		n := c.Add(1)
		rec.SetGRPCInFlight(service, method, int(n))
		start := time.Now()
		resp, err := handler(ctx, req)
		dur := time.Since(start)

		// Record outcome.
		code := status.Code(err).String()
		registryCode := extractCode(err)
		rec.IncGRPCRequest(service, method, code, registryCode)
		rec.ObserveGRPC(service, method, dur)

		left := c.Add(-1)
		rec.SetGRPCInFlight(service, method, int(left))
		return resp, err
	}
}

// splitFullMethod splits "/livepeer.registry.v1.Resolver/ResolveByAddress"
// into ("Resolver", "ResolveByAddress"). Robust to malformed strings.
func splitFullMethod(full string) (service, method string) {
	full = strings.TrimPrefix(full, "/")
	if i := strings.LastIndex(full, "/"); i >= 0 {
		svc := full[:i]
		method = full[i+1:]
		// Strip the proto package prefix: "livepeer.registry.v1.Resolver" → "Resolver"
		if j := strings.LastIndex(svc, "."); j >= 0 {
			service = svc[j+1:]
		} else {
			service = svc
		}
		return
	}
	return full, ""
}

// loggingInterceptor emits one structured log line per RPC including
// method, latency, status code, and the registry error code (if any).
// Skips chatty health checks at info level (debug only).
func loggingInterceptor(log logger.Logger, version string) grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
		start := time.Now()
		resp, err := handler(ctx, req)
		dur := time.Since(start)
		st := status.Code(err)
		method := info.FullMethod

		level := log.Info
		if strings.Contains(method, "grpc.health.v1.Health/Check") {
			level = log.Debug
		} else if err != nil {
			if st == codes.NotFound || st == codes.InvalidArgument {
				level = log.Warn
			} else if st != codes.OK {
				level = log.Error
			}
		}

		level("rpc",
			"method", method,
			"code", st.String(),
			"registry_code", extractCode(err),
			"duration_ms", dur.Milliseconds(),
			"version", version,
		)
		return resp, err
	}
}

// ----- Standard health service -----

// healthService implements grpc.health.v1.Health with a single overall
// state. We don't surface per-service health because both adapters
// share lifecycle.
type healthService struct {
	healthpb.UnimplementedHealthServer
	mu       sync.RWMutex
	serving  bool
	watchers []chan healthpb.HealthCheckResponse_ServingStatus
}

func newHealthService() *healthService { return &healthService{serving: false} }

// SetServing flips the overall serving status and notifies any active
// Watch streams.
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
