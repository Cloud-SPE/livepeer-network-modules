// Package server wires the HTTP server, route table, and middleware chain.
package server

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"time"

	"github.com/Cloud-SPE/livepeer-network-rewrite/capability-broker/internal/backend"
	"github.com/Cloud-SPE/livepeer-network-rewrite/capability-broker/internal/config"
	"github.com/Cloud-SPE/livepeer-network-rewrite/capability-broker/internal/extractors"
	"github.com/Cloud-SPE/livepeer-network-rewrite/capability-broker/internal/media/encoder"
	mediartmp "github.com/Cloud-SPE/livepeer-network-rewrite/capability-broker/internal/media/rtmp"
	"github.com/Cloud-SPE/livepeer-network-rewrite/capability-broker/internal/modes"
	"github.com/Cloud-SPE/livepeer-network-rewrite/capability-broker/internal/modes/rtmpingresshlsegress"
	"github.com/Cloud-SPE/livepeer-network-rewrite/capability-broker/internal/payment"
	"github.com/Cloud-SPE/livepeer-network-rewrite/capability-broker/internal/server/middleware"
)

// Options aggregates non-host-config knobs the server takes at
// construction time. Per-request behavior knobs (e.g. plan 0015's
// interim-debit cadence) live here so they don't pollute the
// host-config.yaml grammar — operators set them via CLI flags.
type Options struct {
	// InterimDebit governs the long-running session ticker per plan
	// 0015. Zero values are a safe disabled state (v0.2 single-debit
	// fall-through).
	InterimDebit middleware.InterimDebitConfig

	// RTMP configures the broker's RTMP ingest listener. Empty Addr
	// keeps the listener disabled; the broker still serves the
	// session-open POST so configurations without RTMP capabilities
	// keep working.
	RTMP RTMPOptions

	// RTMPDriver carries the URL-derivation knobs for the
	// rtmp-ingress-hls-egress driver. Empty values fall back to
	// host-of-backend.url.
	RTMPDriver rtmpingresshlsegress.Config

	// FFmpeg configures the per-session encoder subprocess. Empty
	// Binary defaults to "ffmpeg"; CancelGrace defaults to 5s.
	FFmpeg FFmpegOptions
}

// RTMPOptions configures the broker's RTMP ingest listener.
type RTMPOptions struct {
	Addr             string
	MaxConcurrent    int
	IdleTimeout      time.Duration
	DuplicatePolicy  mediartmp.DuplicatePolicy
	RequireStreamKey bool
}

// FFmpegOptions configures the per-session FFmpeg subprocess.
type FFmpegOptions struct {
	Binary      string
	CancelGrace time.Duration
	// Codec is the encoder selected at startup by media/encoder.Probe.
	// Empty when the RTMP listener is disabled.
	Codec encoder.Codec
}

// Server wraps the broker's HTTP server. It owns two listeners: the paid
// listener (cfg.Listen.Paid) for /v1/cap and /registry/*, and a metrics
// listener (cfg.Listen.Metrics) for Prometheus scraping.
type Server struct {
	cfg          *config.Config
	opts         Options
	mux          *http.ServeMux
	srv          *http.Server
	metricsSrv   *http.Server
	payment      payment.Client
	modes        *modes.Registry
	extractors   *extractors.Registry
	backend      backend.Forwarder
	secrets      backend.SecretResolver
	rtmpStore    *rtmpingresshlsegress.Store
	rtmpListener *mediartmp.Listener
}

// New constructs a Server from a validated config and registers routes. It
// fails-fast if any configured capability references an unregistered mode or
// extractor, since those would be unservable at runtime.
//
// Selection of the payment client follows host-config:
//   - payment_daemon.mock: true       → in-process payment.Mock (tests only)
//   - payment_daemon.socket: <path>   → real gRPC client over unix socket
//   - neither set                     → in-process payment.Mock (legacy default)
//
// When the gRPC client is selected, New calls Health on the daemon and
// fails fast if it is unreachable; the broker should not bind its paid
// listener with no working payment surface.
func New(cfg *config.Config, opts Options) (*Server, error) {
	mux := http.NewServeMux()
	srv := &http.Server{
		Addr:              cfg.Listen.Paid,
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
	}

	paymentClient, err := newPaymentClient(cfg)
	if err != nil {
		return nil, fmt.Errorf("payment client: %w", err)
	}

	rtmpStore := rtmpingresshlsegress.NewStore()
	rtmpDriver := rtmpingresshlsegress.New(rtmpStore, opts.RTMPDriver)

	s := &Server{
		cfg:        cfg,
		opts:       opts,
		mux:        mux,
		srv:        srv,
		payment:    paymentClient,
		modes:      defaultModes(rtmpDriver),
		extractors: defaultExtractors(),
		backend:    backend.NewHTTPClient(),
		secrets:    backend.NewEnvSecretResolver(),
		rtmpStore:  rtmpStore,
	}

	if opts.RTMP.Addr != "" {
		s.rtmpListener = mediartmp.New(mediartmp.Config{
			Addr:             opts.RTMP.Addr,
			MaxConcurrent:    opts.RTMP.MaxConcurrent,
			DuplicatePolicy:  opts.RTMP.DuplicatePolicy,
			RequireStreamKey: opts.RTMP.RequireStreamKey,
		}, &storeLookup{store: rtmpStore})
	}

	if err := s.validateAgainstRegistries(); err != nil {
		return nil, err
	}

	s.registerRoutes()
	s.metricsSrv = newMetricsServer(cfg.Listen.Metrics)
	return s, nil
}

// storeLookup adapts the session store to the mediartmp.SessionLookup
// interface. C1's listener accepts publishes against the store and
// drops bytes into a discard sink — the real encoder wiring lands in
// later commits.
type storeLookup struct {
	store *rtmpingresshlsegress.Store
}

func (l *storeLookup) LookupAndAccept(sessionID, streamKey string) (mediartmp.Sink, bool, bool) {
	rec, ok := l.store.Lookup(sessionID, streamKey)
	if !ok {
		return nil, false, false
	}
	prior, _ := l.store.MarkPublishing(rec.SessionID, time.Now())
	sink := mediartmp.NewDiscardSink()
	return sinkAdapter{base: sink, store: l.store, sessionID: rec.SessionID}, true, prior
}

// sinkAdapter forwards Sink calls to a base sink and pushes Touch
// timestamps into the session store. Lets the listener stay agnostic
// of the rtmpingresshlsegress.Store type.
type sinkAdapter struct {
	base      mediartmp.Sink
	store     *rtmpingresshlsegress.Store
	sessionID string
}

func (s sinkAdapter) WriteFLV(p []byte) (int, error) { return s.base.WriteFLV(p) }
func (s sinkAdapter) Close() error                   { return s.base.Close() }
func (s sinkAdapter) Touch(now time.Time) {
	s.base.Touch(now)
	s.store.Touch(s.sessionID, now)
}

// newPaymentClient picks the right Client implementation per host-config.
func newPaymentClient(cfg *config.Config) (payment.Client, error) {
	switch {
	case cfg.PaymentDaemon.Mock:
		log.Printf("payment client: in-process Mock (payment_daemon.mock=true)")
		return payment.NewMock(), nil
	case cfg.PaymentDaemon.Socket != "":
		log.Printf("payment client: gRPC unix socket %s", cfg.PaymentDaemon.Socket)
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		return payment.NewGRPC(ctx, cfg.PaymentDaemon.Socket)
	default:
		log.Printf("payment client: in-process Mock (no payment_daemon configured)")
		return payment.NewMock(), nil
	}
}

// validateAgainstRegistries fails-fast if any configured capability
// references an unregistered mode or extractor.
func (s *Server) validateAgainstRegistries() error {
	for i := range s.cfg.Capabilities {
		c := &s.cfg.Capabilities[i]
		if !s.modes.Has(c.InteractionMode) {
			return fmt.Errorf("capability %s/%s: interaction_mode %q is not implemented by this broker (registered: %v)",
				c.ID, c.OfferingID, c.InteractionMode, s.modes.Names())
		}
		extractorType, _ := c.WorkUnit.Extractor["type"].(string)
		if !s.extractors.Has(extractorType) {
			return fmt.Errorf("capability %s/%s: work_unit.extractor.type %q is not implemented by this broker (registered: %v)",
				c.ID, c.OfferingID, extractorType, s.extractors.Names())
		}
	}
	return nil
}

// Run starts the server in the foreground. Blocks until ctx is canceled or
// any listener errors; performs graceful shutdown on cancellation.
func (s *Server) Run(ctx context.Context) error {
	errCh := make(chan error, 3)
	go func() {
		log.Printf("listening on %s (paid)", s.cfg.Listen.Paid)
		if err := s.srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- fmt.Errorf("listen paid: %w", err)
			return
		}
		errCh <- nil
	}()
	go func() {
		log.Printf("listening on %s (metrics)", s.cfg.Listen.Metrics)
		if err := s.metricsSrv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- fmt.Errorf("listen metrics: %w", err)
			return
		}
		errCh <- nil
	}()
	if s.rtmpListener != nil {
		go func() {
			if err := s.rtmpListener.Run(ctx); err != nil {
				errCh <- fmt.Errorf("listen rtmp: %w", err)
				return
			}
			errCh <- nil
		}()
	}

	select {
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		_ = s.srv.Shutdown(shutdownCtx)
		_ = s.metricsSrv.Shutdown(shutdownCtx)
		return ctx.Err()
	case err := <-errCh:
		_ = s.srv.Close()
		_ = s.metricsSrv.Close()
		return err
	}
}
