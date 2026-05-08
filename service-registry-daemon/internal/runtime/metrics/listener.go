package metrics

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/Cloud-SPE/livepeer-network-rewrite/service-registry-daemon/internal/providers/logger"
	"github.com/Cloud-SPE/livepeer-network-rewrite/service-registry-daemon/internal/providers/metrics"
)

// Config captures the listener parameters.
type Config struct {
	// Addr is "host:port". Empty means the listener is disabled and
	// NewListener returns nil.
	Addr string

	// Path is the metrics URL path. Defaults to "/metrics".
	Path string

	// Recorder is the metrics provider. Required when Addr is non-empty.
	Recorder metrics.Recorder

	// Logger receives lifecycle logs.
	Logger logger.Logger

	// ReadHeaderTimeout caps slowloris-style attacks. Defaults to 5s.
	ReadHeaderTimeout time.Duration
}

// Listener owns the TCP listener and HTTP server. Construction is
// fallible (network bind); shutdown is graceful via context.
type Listener struct {
	cfg   Config
	srv   *http.Server
	netLn net.Listener
	once  sync.Once
}

// NewListener builds the HTTP server but does NOT yet bind. Returns
// nil (no listener) if cfg.Addr is empty — caller treats that as
// "metrics disabled". Returns an error for any other malformed
// configuration.
func NewListener(cfg Config) (*Listener, error) {
	if cfg.Addr == "" {
		return nil, nil
	}
	if cfg.Recorder == nil {
		return nil, errors.New("metrics listener: nil Recorder")
	}
	if cfg.Path == "" {
		cfg.Path = "/metrics"
	}
	if cfg.Logger == nil {
		cfg.Logger = logger.Discard()
	}
	if cfg.ReadHeaderTimeout <= 0 {
		cfg.ReadHeaderTimeout = 5 * time.Second
	}

	mux := http.NewServeMux()
	mux.Handle(cfg.Path, cfg.Recorder.Handler())
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok\n"))
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/" {
			w.WriteHeader(http.StatusOK)
			_, _ = fmt.Fprintf(w, "livepeer-service-registry metrics\n\nendpoints:\n  %s\n  /healthz\n", cfg.Path)
			return
		}
		http.NotFound(w, r)
	})

	srv := &http.Server{
		Addr:              cfg.Addr,
		Handler:           mux,
		ReadHeaderTimeout: cfg.ReadHeaderTimeout,
	}
	return &Listener{cfg: cfg, srv: srv}, nil
}

// Serve binds the TCP socket and serves until ctx cancellation. The
// blocking model mirrors runtime/grpc/listener.go: ctx.Done triggers
// Stop, Stop's GracefulShutdown drains, and Serve returns.
func (l *Listener) Serve(ctx context.Context) error {
	if l == nil {
		<-ctx.Done()
		return nil
	}
	ln, err := net.Listen("tcp", l.cfg.Addr)
	if err != nil {
		return fmt.Errorf("metrics listener: bind %s: %w", l.cfg.Addr, err)
	}
	l.netLn = ln
	l.cfg.Logger.Info("metrics listening", "addr", ln.Addr().String(), "path", l.cfg.Path)

	stopErr := make(chan error, 1)
	stopped := make(chan struct{})
	go func() {
		<-ctx.Done()
		l.Stop()
		close(stopped)
	}()
	go func() {
		stopErr <- l.srv.Serve(ln)
	}()

	select {
	case err = <-stopErr:
	case <-stopped:
		select {
		case err = <-stopErr:
		case <-time.After(100 * time.Millisecond):
			err = http.ErrServerClosed
		}
	}
	if err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

// Stop performs a graceful shutdown with a 2s cap. Idempotent.
func (l *Listener) Stop() {
	if l == nil {
		return
	}
	l.once.Do(func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		if err := l.srv.Shutdown(shutdownCtx); err != nil {
			l.cfg.Logger.Warn("metrics shutdown error", "err", err)
		}
		if l.netLn != nil {
			_ = l.netLn.Close()
		}
		l.cfg.Logger.Info("metrics stopped")
	})
}

// Addr returns the listener's resolved address (empty before Serve).
func (l *Listener) Addr() string {
	if l == nil || l.netLn == nil {
		return ""
	}
	return l.netLn.Addr().String()
}
