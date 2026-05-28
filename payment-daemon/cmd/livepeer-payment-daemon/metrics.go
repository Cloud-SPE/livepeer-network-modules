package main

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"runtime"
	"time"

	"github.com/Cloud-SPE/livepeer-network-modules/payment-daemon/internal/providers/metrics"
)

// newRecorder builds the metrics Recorder for the run. When
// --metrics-listen is empty it returns a zero-cost Noop. Otherwise it
// builds the Prometheus recorder, stamps build info, starts the uptime
// ticker, and serves /metrics on a goroutine bound to ctx.
func newRecorder(ctx context.Context, logger *slog.Logger, cfg bootConfig) metrics.Recorder {
	if cfg.metricsListen == "" {
		return metrics.NewNoop()
	}
	p := metrics.NewPrometheus()
	p.SetBuildInfo(version, cfg.mode, runtime.Version())

	start := time.Now()
	p.SetUptimeSeconds(0)
	go func() {
		t := time.NewTicker(15 * time.Second)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				p.SetUptimeSeconds(time.Since(start).Seconds())
			}
		}
	}()

	go serveMetrics(ctx, logger.With("component", "metrics"), cfg.metricsListen, p.Handler())
	return p
}

// serveMetrics runs the /metrics HTTP listener until ctx is cancelled.
// A bind failure is logged (not fatal): the daemon's core gRPC duty
// continues even if the observability listener can't start.
func serveMetrics(ctx context.Context, logger *slog.Logger, addr string, handler http.Handler) {
	mux := http.NewServeMux()
	mux.Handle("/metrics", handler)
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok\n"))
	})
	srv := &http.Server{
		Addr:              addr,
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
	}

	ln, err := net.Listen("tcp", addr)
	if err != nil {
		logger.Error("metrics listener bind failed; metrics disabled for this run", "addr", addr, "err", err)
		return
	}
	logger.Info("metrics listening", "addr", ln.Addr().String(), "path", "/metrics")

	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutdownCtx)
	}()

	if err := srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
		logger.Warn("metrics server stopped", "err", err)
	}
}
