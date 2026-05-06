// Package metrics is the coordinator's Prometheus surface per
// plan 0018 §10. Counters / histograms / gauges are cardinality-
// capped: broker labels come from the static config (bounded), and
// outcome / drift labels are pinned enums.
package metrics

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// Registry bundles the coordinator's metric handles. Built once at
// boot and shared across services.
type Registry struct {
	reg *prometheus.Registry

	// Counters.
	ScrapeTotal          *prometheus.CounterVec
	CandidateBuildTotal  *prometheus.CounterVec
	SignedUploadsTotal   *prometheus.CounterVec
	PublishesTotal       *prometheus.CounterVec

	// Histograms.
	ScrapeDuration         *prometheus.HistogramVec
	CandidateBuildDuration prometheus.Histogram
	SignedVerifyDuration   prometheus.Histogram

	// Gauges.
	KnownBrokers         prometheus.Gauge
	BrokersHealthy       prometheus.Gauge
	ManifestAgeSeconds   prometheus.Gauge
	CapabilityTuples     prometheus.Gauge
	CandidateDriftCount  *prometheus.GaugeVec
}

// New builds a Registry.
func New() *Registry {
	r := &Registry{reg: prometheus.NewRegistry()}
	r.ScrapeTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "orch_coordinator_scrape_total",
		Help: "Broker /registry/offerings scrape attempts by outcome.",
	}, []string{"broker", "outcome"})
	r.CandidateBuildTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "orch_coordinator_candidate_builds_total",
		Help: "Candidate build attempts by outcome.",
	}, []string{"outcome"})
	r.SignedUploadsTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "orch_coordinator_signed_uploads_total",
		Help: "Signed-manifest upload attempts by outcome.",
	}, []string{"outcome"})
	r.PublishesTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "orch_coordinator_publishes_total",
		Help: "Atomic-swap publish attempts by outcome.",
	}, []string{"outcome"})

	r.ScrapeDuration = prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "orch_coordinator_scrape_duration_seconds",
		Help:    "Per-broker scrape wall-clock duration.",
		Buckets: prometheus.DefBuckets,
	}, []string{"broker"})
	r.CandidateBuildDuration = prometheus.NewHistogram(prometheus.HistogramOpts{
		Name:    "orch_coordinator_candidate_build_duration_seconds",
		Help:    "Candidate build wall-clock duration.",
		Buckets: prometheus.DefBuckets,
	})
	r.SignedVerifyDuration = prometheus.NewHistogram(prometheus.HistogramOpts{
		Name:    "orch_coordinator_signed_verify_duration_seconds",
		Help:    "Signed-manifest verify wall-clock duration.",
		Buckets: prometheus.DefBuckets,
	})

	r.KnownBrokers = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "orch_coordinator_known_brokers",
		Help: "Total brokers configured in coordinator-config.yaml.",
	})
	r.BrokersHealthy = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "orch_coordinator_brokers_healthy",
		Help: "Brokers whose last scrape succeeded.",
	})
	r.ManifestAgeSeconds = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "orch_coordinator_published_manifest_age_seconds",
		Help: "Seconds since the currently-published manifest's issued_at; -1 when no manifest is published.",
	})
	r.CapabilityTuples = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "orch_coordinator_published_capability_tuples",
		Help: "Number of capability tuples in the currently-published manifest.",
	})
	r.CandidateDriftCount = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "orch_coordinator_candidate_drift_count",
		Help: "Pending-publish drift count by kind.",
	}, []string{"kind"})

	for _, c := range []prometheus.Collector{
		r.ScrapeTotal, r.CandidateBuildTotal, r.SignedUploadsTotal, r.PublishesTotal,
		r.ScrapeDuration, r.CandidateBuildDuration, r.SignedVerifyDuration,
		r.KnownBrokers, r.BrokersHealthy, r.ManifestAgeSeconds, r.CapabilityTuples, r.CandidateDriftCount,
	} {
		r.reg.MustRegister(c)
	}
	return r
}

// Handler returns the HTTP handler for /metrics.
func (r *Registry) Handler() http.Handler {
	return promhttp.HandlerFor(r.reg, promhttp.HandlerOpts{})
}

// Server runs the dedicated metrics listener.
type Server struct {
	addr   string
	r      *Registry
	logger *slog.Logger

	mu       sync.Mutex
	listener net.Listener
	httpSrv  *http.Server
}

// NewServer wires the metrics listener.
func NewServer(addr string, r *Registry, logger *slog.Logger) *Server {
	if logger == nil {
		logger = slog.Default()
	}
	return &Server{addr: addr, r: r, logger: logger}
}

// Listen binds the metrics listener.
func (s *Server) Listen() (net.Addr, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.listener != nil {
		return s.listener.Addr(), nil
	}
	ln, err := net.Listen("tcp", s.addr)
	if err != nil {
		return nil, fmt.Errorf("metrics: listen %s: %w", s.addr, err)
	}
	s.listener = ln
	mux := http.NewServeMux()
	mux.Handle("GET /metrics", s.r.Handler())
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("ok\n"))
	})
	s.httpSrv = &http.Server{
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
	}
	return ln.Addr(), nil
}

// Serve runs until ctx cancellation.
func (s *Server) Serve(ctx context.Context) error {
	if _, err := s.Listen(); err != nil {
		return err
	}
	go func() {
		<-ctx.Done()
		shutCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = s.httpSrv.Shutdown(shutCtx)
	}()
	if err := s.httpSrv.Serve(s.listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

// Addr returns the bound address.
func (s *Server) Addr() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.listener == nil {
		return ""
	}
	return s.listener.Addr().String()
}
