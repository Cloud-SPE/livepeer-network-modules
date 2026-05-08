package metrics

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/Cloud-SPE/livepeer-network-rewrite/chain-commons/providers/logger"
)

// Config wires the Prometheus listener.
type Config struct {
	// Addr is the host:port to bind. Empty = no listener (caller skips).
	Addr string

	// Path is the metrics path. Default "/metrics".
	Path string

	// Handler is the Prometheus handler that serves /metrics. Required when
	// Addr is non-empty. Daemons construct this with promhttp.Handler() in
	// cmd; this package keeps the listener pure-stdlib.
	Handler http.Handler

	// Logger is optional; if set, listener events are logged.
	Logger logger.Logger
}

// Listener wraps net/http with start/stop lifecycle.
type Listener struct {
	cfg    Config
	server *http.Server

	mu      sync.Mutex
	started bool
	closed  bool
}

// NewListener returns a Listener configured per cfg. Returns nil with no
// error when cfg.Addr is empty (the caller treats nil as "metrics disabled").
func NewListener(cfg Config) (*Listener, error) {
	if cfg.Addr == "" {
		return nil, nil
	}
	if cfg.Handler == nil {
		return nil, errors.New("metrics: Handler is required when Addr is set")
	}
	if cfg.Path == "" {
		cfg.Path = "/metrics"
	}

	mux := http.NewServeMux()
	mux.Handle(cfg.Path, cfg.Handler)
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})

	return &Listener{
		cfg: cfg,
		server: &http.Server{
			Addr:              cfg.Addr,
			Handler:           mux,
			ReadHeaderTimeout: 5 * time.Second,
		},
	}, nil
}

// Serve binds the listener and serves until ctx is cancelled. Blocks.
func (l *Listener) Serve(ctx context.Context) error {
	l.mu.Lock()
	if l.started {
		l.mu.Unlock()
		return errors.New("metrics: listener already started")
	}
	l.started = true
	l.mu.Unlock()

	ln, err := net.Listen("tcp", l.cfg.Addr)
	if err != nil {
		return fmt.Errorf("metrics: listen %s: %w", l.cfg.Addr, err)
	}
	if l.cfg.Logger != nil {
		l.cfg.Logger.Info("metrics.listener_started",
			logger.String("addr", ln.Addr().String()),
		)
	}

	errCh := make(chan error, 1)
	go func() {
		errCh <- l.server.Serve(ln)
	}()

	select {
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = l.server.Shutdown(shutdownCtx)
		l.mu.Lock()
		l.closed = true
		l.mu.Unlock()
		if l.cfg.Logger != nil {
			l.cfg.Logger.Info("metrics.listener_stopped")
		}
		return ctx.Err()
	case err := <-errCh:
		l.mu.Lock()
		l.closed = true
		l.mu.Unlock()
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	}
}

// Closed reports whether Serve has exited.
func (l *Listener) Closed() bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.closed
}
