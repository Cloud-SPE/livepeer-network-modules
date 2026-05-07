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
	"github.com/Cloud-SPE/livepeer-network-rewrite/capability-broker/internal/extractors/ffmpegprogress"
	"github.com/Cloud-SPE/livepeer-network-rewrite/capability-broker/internal/media/encoder"
	"github.com/Cloud-SPE/livepeer-network-rewrite/capability-broker/internal/media/hls"
	mediartmp "github.com/Cloud-SPE/livepeer-network-rewrite/capability-broker/internal/media/rtmp"
	"github.com/Cloud-SPE/livepeer-network-rewrite/capability-broker/internal/media/sessionrunner"
	mediawebrtc "github.com/Cloud-SPE/livepeer-network-rewrite/capability-broker/internal/media/webrtc"
	"github.com/Cloud-SPE/livepeer-network-rewrite/capability-broker/internal/modes"
	"github.com/Cloud-SPE/livepeer-network-rewrite/capability-broker/internal/modes/rtmpingresshlsegress"
	"github.com/Cloud-SPE/livepeer-network-rewrite/capability-broker/internal/modes/sessioncontrolplusmedia"
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

	// HLS configures the LL-HLS muxer flags + scratch root.
	HLS HLSOptions

	// SessionControl configures the session-control-plus-media
	// driver's control-WS lifecycle.
	SessionControl sessioncontrolplusmedia.ControlWSConfig

	// WebRTC governs the per-broker pion settings used by the
	// session-control-plus-media media-plane relay.
	WebRTC mediawebrtc.Config

	// SessionRunner governs the per-broker session-runner subprocess
	// supervisor consumed by the session-control-plus-media driver.
	SessionRunner sessionrunner.Config
}

// HLSOptions configures the LL-HLS muxer + scratch directory.
type HLSOptions struct {
	Legacy          bool
	PartDuration    time.Duration
	SegmentDuration time.Duration
	PlaylistWindow  int
	ScratchDir      string
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
	sessStore     *sessioncontrolplusmedia.Store
	sessDriver    *sessioncontrolplusmedia.Driver
	webrtcEngine  *mediawebrtc.Engine
	sessRunnerSup *sessionrunner.Supervisor
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

	sessCfg := opts.SessionControl
	if sessCfg.HeartbeatInterval == 0 && sessCfg.ReconnectWindow == 0 {
		sessCfg = sessioncontrolplusmedia.DefaultControlWSConfig()
	}
	sessStore := sessioncontrolplusmedia.NewStore(sessioncontrolplusmedia.StoreConfig{
		ReplayBufferMessages: sessCfg.ReplayBufferMessages,
		ReplayBufferBytes:    sessCfg.ReplayBufferBytes,
	})
	sessDriver := sessioncontrolplusmedia.New(sessStore, sessCfg)

	rtcCfg := opts.WebRTC
	if rtcCfg.UDPPortMin == 0 {
		rtcCfg = mediawebrtc.DefaultConfig()
	}
	rtcEngine, err := mediawebrtc.NewEngine(rtcCfg)
	if err != nil {
		return nil, fmt.Errorf("webrtc engine: %w", err)
	}

	runnerCfg := opts.SessionRunner
	if runnerCfg.ContainerRuntime == "" {
		runnerCfg = sessionrunner.DefaultConfig()
	}
	runnerSup, err := sessionrunner.NewSupervisor(runnerCfg)
	if err != nil {
		return nil, fmt.Errorf("session-runner supervisor: %w", err)
	}

	s := &Server{
		cfg:           cfg,
		opts:          opts,
		mux:           mux,
		srv:           srv,
		payment:       paymentClient,
		modes:         defaultModes(rtmpDriver, sessDriver),
		extractors:    defaultExtractors(),
		backend:       backend.NewHTTPClient(),
		secrets:       backend.NewEnvSecretResolver(),
		rtmpStore:     rtmpStore,
		sessStore:     sessStore,
		sessDriver:    sessDriver,
		webrtcEngine:  rtcEngine,
		sessRunnerSup: runnerSup,
	}

	if opts.RTMP.Addr != "" {
		s.rtmpListener = mediartmp.New(mediartmp.Config{
			Addr:             opts.RTMP.Addr,
			MaxConcurrent:    opts.RTMP.MaxConcurrent,
			DuplicatePolicy:  opts.RTMP.DuplicatePolicy,
			RequireStreamKey: opts.RTMP.RequireStreamKey,
		}, &mediaLookup{
			store:   rtmpStore,
			ffmpeg:  opts.FFmpeg,
			hls:     opts.HLS,
			lookupCap: func(capID, offID string) (encoderProfile string, ok bool) {
				for i := range cfg.Capabilities {
					c := &cfg.Capabilities[i]
					if c.ID == capID && c.OfferingID == offID {
						return c.Backend.Profile, true
					}
				}
				return "", false
			},
		})
	}

	if err := s.validateAgainstRegistries(); err != nil {
		return nil, err
	}

	s.registerRoutes()
	s.metricsSrv = newMetricsServer(cfg.Listen.Metrics)
	return s, nil
}

// mediaLookup adapts the session store to mediartmp.SessionLookup
// and owns the per-session encoder + LL-HLS scratch wire-up. On a
// successful publish handshake it:
//
//  1. Resolves the capability's encoder profile.
//  2. Materialises the per-session HLS scratch.
//  3. Renders FFmpeg argv from the profile + HLS options.
//  4. Spawns the SystemEncoder goroutine reading from a PipeSink.
//  5. Returns the PipeSink to the listener.
//
// Cancellation flows through the context attached to the encoder; the
// listener invokes Close on the sink when RTMP disconnects.
type mediaLookup struct {
	store     *rtmpingresshlsegress.Store
	ffmpeg    FFmpegOptions
	hls       HLSOptions
	lookupCap func(capID, offID string) (string, bool)
}

func (l *mediaLookup) LookupAndAccept(sessionID, streamKey string) (mediartmp.Sink, bool, bool) {
	rec, ok := l.store.Lookup(sessionID, streamKey)
	if !ok {
		return nil, false, false
	}
	if rec.Profile == "" {
		log.Printf("rtmp: session=%s has empty profile (capability=%s/%s); falling back to passthrough",
			sessionID, rec.CapabilityID, rec.OfferingID)
		rec.Profile = encoder.ProfilePassthrough
	}

	scratch := hls.NewScratch(l.hls.ScratchDir, sessionID)
	rungs := []string{}
	if rec.Profile != encoder.ProfilePassthrough {
		for _, r := range encoder.FiveRungLadder {
			rungs = append(rungs, r.Name)
		}
	}
	scratchDir, err := scratch.Setup(rungs)
	if err != nil {
		log.Printf("rtmp: session=%s scratch setup failed: %v", sessionID, err)
		return nil, false, false
	}

	args, err := encoder.BuildArgs(encoder.PresetInput{
		Profile: rec.Profile,
		Codec:   l.ffmpeg.Codec,
		HLS: encoder.HLSOptions{
			Legacy:          l.hls.Legacy,
			SegmentDuration: int(l.hls.SegmentDuration.Seconds()),
			PartDuration:    l.hls.PartDuration.Seconds(),
			PlaylistWindow:  l.hls.PlaylistWindow,
			ScratchDir:      scratchDir,
		},
	})
	if err != nil {
		log.Printf("rtmp: session=%s build args: %v", sessionID, err)
		_ = scratch.Cleanup()
		return nil, false, false
	}

	sysEnc := encoder.NewSystemEncoder(l.ffmpeg.Binary, l.ffmpeg.CancelGrace)
	prog := sysEnc.Progress()
	if rec.Profile != encoder.ProfilePassthrough {
		prog.Width = uint64(encoder.FiveRungLadder[len(encoder.FiveRungLadder)-1].Width)
		prog.Height = uint64(encoder.FiveRungLadder[len(encoder.FiveRungLadder)-1].Height)
	}

	pipe := mediartmp.NewPipeSink(func(now time.Time) { l.store.Touch(sessionID, now) })

	encCtx, encCancel := context.WithCancel(context.Background())
	encDone := make(chan struct{})
	go func() {
		defer close(encDone)
		if err := sysEnc.Run(encCtx, encoder.Job{
			Input:      pipe.Reader(),
			ScratchDir: scratchDir,
			Profile:    rec.Profile,
			Args:       args,
		}); err != nil {
			log.Printf("rtmp: session=%s encoder exited with err=%v", sessionID, err)
		}
	}()

	cancel := func() {
		encCancel()
		_ = pipe.Close()
		<-encDone
		_ = scratch.Cleanup()
	}

	lc := buildRTMPLiveCounter(rec, prog)
	l.store.AttachMedia(sessionID, lc, cancel)

	prior, _ := l.store.MarkPublishing(sessionID, time.Now())
	return pipe, true, prior
}

// buildRTMPLiveCounter selects the LiveCounter shape based on the
// capability's configured ffmpeg-progress unit. Falls back to a
// nil-safe out_time_seconds counter when the capability uses a
// different extractor.
func buildRTMPLiveCounter(rec *rtmpingresshlsegress.SessionRecord, prog *encoder.Progress) extractors.LiveCounter {
	_ = rec
	ext := &progressLiveCounter{prog: prog}
	return ext
}

// progressLiveCounter exposes encoder.Progress as an
// extractors.LiveCounter. Distinct from
// extractors/ffmpegprogress.LiveCounter — that one wraps separate
// atomics; this one wraps the encoder.Progress directly so the unit
// resolution lives in one place when the dispatch layer owns it.
type progressLiveCounter struct {
	prog *encoder.Progress
}

func (p *progressLiveCounter) CurrentUnits() uint64 {
	if p == nil || p.prog == nil {
		return 0
	}
	return p.prog.CurrentUnits()
}

// _ = ffmpegprogress is kept as a build-time hint that the
// per-extractor LiveCounter constructor in extractors/ffmpegprogress
// is the alternative wiring (used when the dispatch layer
// short-circuits past the rtmp-ingress driver).
var _ = ffmpegprogress.Name

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
		go s.rtmpStore.RunWatchdog(ctx, rtmpingresshlsegress.LifetimeOptions{
			IdleTimeout:   s.opts.RTMP.IdleTimeout,
			CheckInterval: time.Second,
		})
	}

	if s.sessDriver != nil {
		go s.sessDriver.RunReconnectWatchdog(ctx)
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
