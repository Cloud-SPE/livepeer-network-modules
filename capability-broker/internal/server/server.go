// Package server wires the HTTP server, route table, and middleware chain.
package server

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"log"
	"math/rand"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/Cloud-SPE/livepeer-network-modules/capability-broker/internal/backend"
	"github.com/Cloud-SPE/livepeer-network-modules/capability-broker/internal/config"
	"github.com/Cloud-SPE/livepeer-network-modules/capability-broker/internal/extractors"
	"github.com/Cloud-SPE/livepeer-network-modules/capability-broker/internal/health"
	"github.com/Cloud-SPE/livepeer-network-modules/capability-broker/internal/payment"
	"github.com/Cloud-SPE/livepeer-network-modules/capability-broker/internal/poolreport"
	"github.com/Cloud-SPE/livepeer-network-modules/capability-broker/internal/poolsnapshot"
	"github.com/Cloud-SPE/livepeer-network-modules/capability-broker/internal/receipts"
	"github.com/Cloud-SPE/livepeer-network-modules/capability-broker/internal/server/middleware"
	"github.com/Cloud-SPE/livepeer-network-modules/capability-broker/internal/sessionengine"
	"github.com/Cloud-SPE/livepeer-network-modules/capability-broker/internal/sessionstore"
	"github.com/Cloud-SPE/livepeer-network-modules/capability-broker/internal/settlement"
	"github.com/Cloud-SPE/livepeer-network-modules/capability-broker/internal/workerconn"
)

// Options aggregates non-host-config knobs the server takes at
// construction time. Per-request behavior knobs (e.g. plan 0015's
// interim-debit cadence) live here so they don't pollute the
// host-config.yaml grammar — operators set them via CLI flags.
type Options struct {
	ConfigPath string

	// MetadataRefreshInterval controls periodic stable-metadata refresh for
	// discovery-capable offerings. Zero falls back to the broker default.
	// Negative values disable periodic refresh after the initial bootstrap pass.
	MetadataRefreshInterval time.Duration

	// InterimDebit governs the long-running session ticker per plan
	// 0015. Zero values are a safe disabled state (v0.2 single-debit
	// fall-through).
	InterimDebit middleware.InterimDebitConfig

	// PaymentClient replaces the client the config would select. Tests
	// use it to drive failure paths the config cannot reach — a ledger
	// that refuses a debit after the work has already shipped is the
	// case the durable-retry lifecycle exists for, and it is
	// unreachable without injecting the failure.
	PaymentClient payment.Client
}

// Server wraps the broker's HTTP server. It owns two listeners: the paid
// listener (cfg.Listen.Paid) for /v1/job, /v1/session/*, /registry/* and
// the admin surface, and a metrics listener (cfg.Listen.Metrics) for
// Prometheus scraping.
type Server struct {
	settlementSigner     *settlement.Signer
	memDebitSeqMu        sync.Mutex
	memDebitSeq          map[string]uint64
	mu                   sync.RWMutex
	cfg                  *config.Config
	configPath           string
	loadedConfigPath     string
	loadedRevision       string
	adminToken           string
	runCtx               context.Context
	healthCancel         context.CancelFunc
	loadedAt             time.Time
	lastReloadAttemptID  string
	lastReloadStartedAt  time.Time
	lastReloadFinishedAt time.Time
	lastReloadStatus     string
	lastReloadError      string
	reloadHistory        []runtimeHistoryEntry
	opts                 Options
	metadata             *metadataCatalog
	mux                  *http.ServeMux
	srv                  *http.Server
	metricsSrv           *http.Server
	payment              payment.Client
	extractors           *extractors.Registry
	backend              backend.Forwarder
	workerRegistry       *workerconn.Registry
	backendInFlight      map[string]int
	secrets              backend.SecretResolver
	receiptSink          receipts.Client
	poolReporter         poolreport.Client
	poolSnapshot         *poolsnapshot.Cache
	health               *health.Manager
	sessionStore         *sessionstore.Store
	sessionEngine        *sessionengine.Engine
	sessionWS            *sessionWSHub
	jobIdem              jobIdemStore
	// quarantined holds capability tuples ("cap|off") the broker will
	// not serve or advertise, with the reason. Populated when a runner's
	// self-description contradicts its configuration (paid-session
	// §7.1.1) — fatal to that capability, not to the broker.
	quarantined map[string]string
	randIntn    func(int) int
}

// New constructs a Server from a validated config and registers routes. It
// fails-fast if any configured capability references an unregistered
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
	metadata := newMetadataCatalog()
	refreshMetadataCatalog(context.Background(), &http.Client{Timeout: 2 * time.Second}, cfg, metadata)
	loadedRevision, loadedConfigPath, err := loadRuntimeRevision(opts.ConfigPath, cfg)
	if err != nil {
		return nil, err
	}
	loadedAt := time.Now().UTC()
	adminToken, err := resolveAdminToken(cfg)
	if err != nil {
		return nil, err
	}

	mux := http.NewServeMux()
	srv := &http.Server{
		Addr:              cfg.Listen.Paid,
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
	}

	// An injected client wins over the configured one, so a test can
	// drive failure paths the config cannot express.
	paymentClient := opts.PaymentClient
	if paymentClient == nil {
		var err error
		paymentClient, err = newPaymentClient(cfg)
		if err != nil {
			return nil, fmt.Errorf("payment client: %w", err)
		}
	}
	secretResolver := backend.NewEnvSecretResolver()
	receiptSink, err := newReceiptSink(cfg, secretResolver)
	if err != nil {
		return nil, fmt.Errorf("receipt sink: %w", err)
	}
	poolReporter, err := newPoolReporter(cfg, secretResolver)
	if err != nil {
		return nil, fmt.Errorf("pool outcome reporter: %w", err)
	}
	poolSnapshot, err := poolsnapshot.New(cfg.PoolSnapshot, backend.NewAuthApplier(secretResolver))
	if err != nil {
		return nil, fmt.Errorf("pool snapshot: %w", err)
	}

	// The delegated settlement key, if the operator published one.
	// Absent is not an error: a broker on a mock payment layer has no
	// delegation and still has to report what it billed.
	var settlementSigner *settlement.Signer
	if path := cfg.Identity.SettlementKeyFile; path != "" {
		settlementSigner, err = settlement.LoadSigner(path)
		if err != nil {
			return nil, fmt.Errorf("settlement key: %w", err)
		}
		notBefore, expiresAt, verr := parseKeyValidity(cfg.Identity)
		if verr != nil {
			return nil, fmt.Errorf("settlement key validity: %w", verr)
		}
		settlementSigner.SetValidity(notBefore, expiresAt)
		log.Printf("settlement signing enabled; delegated public key %s (validity %s)",
			settlementSigner.PublicKeyHex(), describeValidity(notBefore, expiresAt))
	} else {
		log.Printf("warning: no identity.settlement_key_file — settlement records go out UNSIGNED " +
			"and a clearinghouse will refuse them for anything financially material")
	}

	workerRegistry := workerconn.NewRegistry()

	// Reconcile runner self-descriptions before anything reads the
	// config: a derived readiness probe must be in place when the
	// health manager is constructed below.
	quarantined := applyRunnerDescriptions(cfg)

	s := &Server{
		cfg:                 cfg,
		configPath:          opts.ConfigPath,
		loadedConfigPath:    loadedConfigPath,
		loadedRevision:      loadedRevision,
		adminToken:          adminToken,
		loadedAt:            loadedAt,
		lastReloadAttemptID: "startup",
		lastReloadStatus:    "startup_loaded",
		reloadHistory: []runtimeHistoryEntry{{
			AttemptID:      "startup",
			StartedAt:      loadedAt,
			FinishedAt:     loadedAt,
			Status:         "startup_loaded",
			LoadedRevision: loadedRevision,
		}},
		opts:             opts,
		metadata:         metadata,
		mux:              mux,
		srv:              srv,
		payment:          paymentClient,
		settlementSigner: settlementSigner,
		extractors:       defaultExtractors(),
		backend:          workerconn.NewForwarder(backend.NewHTTPClient(), workerRegistry),
		workerRegistry:   workerRegistry,
		backendInFlight:  make(map[string]int),
		secrets:          secretResolver,
		receiptSink:      receiptSink,
		poolReporter:     poolReporter,
		poolSnapshot:     poolSnapshot,
		health:           health.NewWithTransport(cfg, nil, workerRegistry.HTTPTransport(nil)),
		quarantined:      quarantined,
		randIntn: func(n int) int {
			return rand.New(rand.NewSource(time.Now().UnixNano())).Intn(n)
		},
	}

	if err := s.validateAgainstRegistries(); err != nil {
		return nil, err
	}

	if err := s.initSessionEngine(); err != nil {
		return nil, fmt.Errorf("session engine: %w", err)
	}

	s.registerRoutes()
	if s.sessionEngine != nil {
		s.registerSessionRoutes()
	}
	if err := s.initJobIdem(); err != nil {
		return nil, fmt.Errorf("job idempotency: %w", err)
	}
	if s.jobIdem != nil {
		s.registerJobRoutes()
	}
	s.metricsSrv = newMetricsServer(cfg.Listen.Metrics)
	return s, nil
}

func loadRuntimeRevision(configPath string, cfg *config.Config) (string, string, error) {
	if configPath != "" {
		raw, err := os.ReadFile(configPath)
		if err != nil {
			return "", "", fmt.Errorf("read config %q: %w", configPath, err)
		}
		sum := sha256.Sum256(raw)
		return fmt.Sprintf("%x", sum[:]), configPath, nil
	}
	if cfg == nil {
		return "", "", fmt.Errorf("config is required")
	}
	sum := sha256.Sum256([]byte(cfg.Identity.OrchEthAddress))
	return fmt.Sprintf("%x", sum[:]), "", nil
}

func resolveAdminToken(cfg *config.Config) (string, error) {
	if cfg == nil {
		return "", nil
	}
	switch cfg.AdminAuth.Method {
	case "", "none":
		return "", nil
	case "bearer":
		key := strings.TrimPrefix(strings.TrimSpace(cfg.AdminAuth.SecretRef), "env://")
		value := strings.TrimSpace(os.Getenv(key))
		if value == "" {
			return "", fmt.Errorf("admin auth env var %q is empty", key)
		}
		return value, nil
	default:
		return "", fmt.Errorf("unsupported admin auth method %q", cfg.AdminAuth.Method)
	}
}

func newReceiptSink(cfg *config.Config, secrets backend.SecretResolver) (receipts.Client, error) {
	if cfg.ReceiptSink.URL == "" {
		return nil, nil
	}
	timeout := time.Duration(cfg.ReceiptSink.TimeoutMS) * time.Millisecond
	return receipts.NewHTTPClient(cfg.ReceiptSink.URL, timeout, cfg.ReceiptSink.Auth, backend.NewAuthApplier(secrets))
}

func newPoolReporter(cfg *config.Config, secrets backend.SecretResolver) (poolreport.Client, error) {
	if cfg == nil || cfg.PoolSnapshot.URL == "" {
		return nil, nil
	}
	timeout := time.Duration(cfg.PoolSnapshot.TimeoutMS) * time.Millisecond
	return poolreport.NewHTTPClient(cfg.PoolSnapshot.URL, timeout, cfg.PoolSnapshot.Auth, backend.NewAuthApplier(secrets))
}

// newPaymentClient picks the right Client implementation per host-config.
func newPaymentClient(cfg *config.Config) (payment.Client, error) {
	switch {
	case cfg.PaymentDaemon.Mock:
		mock := payment.NewMock()
		if p := cfg.PaymentDaemon.MockStatePath; p != "" {
			if err := mock.EnablePersistence(p); err != nil {
				return nil, fmt.Errorf("payment mock state: %w", err)
			}
			log.Printf("payment client: in-process Mock, state at %s (survives restart)", p)
		} else {
			log.Printf("payment client: in-process Mock (payment_daemon.mock=true; state is NOT durable)")
		}
		return payment.WithMetrics(mock), nil
	case cfg.PaymentDaemon.Socket != "":
		log.Printf("payment client: gRPC unix socket %s", cfg.PaymentDaemon.Socket)
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		client, err := payment.NewGRPC(ctx, cfg.PaymentDaemon.Socket)
		if err != nil {
			return nil, err
		}
		return payment.WithMetrics(client), nil
	default:
		log.Printf("payment client: in-process Mock (no payment_daemon configured)")
		return payment.WithMetrics(payment.NewMock()), nil
	}
}

// validateAgainstRegistries fails-fast if any configured capability
// references an unregistered extractor.
func (s *Server) validateAgainstRegistries() error {
	cfg := s.currentConfig()
	return validateConfigAgainstRegistries(cfg, s.extractors)
}

func validateConfigAgainstRegistries(cfg *config.Config, extractorRegistry *extractors.Registry) error {
	if cfg == nil {
		return fmt.Errorf("config is not loaded")
	}
	for i := range cfg.Capabilities {
		c := &cfg.Capabilities[i]
		// paid-session capabilities carry no extractor (see config
		// validation): usage is runner-reported.
		if strings.HasPrefix(c.Protocol, "paid-session/") {
			continue
		}
		extractorType, _ := c.WorkUnit.Extractor["type"].(string)
		if !extractorRegistry.Has(extractorType) {
			return fmt.Errorf("capability %s/%s: work_unit.extractor.type %q is not implemented by this broker (registered: %v)",
				c.ID, c.OfferingID, extractorType, extractorRegistry.Names())
		}
	}
	return nil
}

func (s *Server) currentConfig() *config.Config {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.cfg
}

func (s *Server) currentHealth() *health.Manager {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.health
}

func (s *Server) currentMetadata() *metadataCatalog {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.metadata
}

func (s *Server) currentPoolSnapshot() *poolsnapshot.Cache {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.poolSnapshot
}

// Run starts the server in the foreground. Blocks until ctx is canceled or
// any listener errors; performs graceful shutdown on cancellation.
func (s *Server) Run(ctx context.Context) error {
	errCh := make(chan error, 4)
	if s.sessionEngine != nil {
		// Restart recovery first (rebind-or-terminal), then the
		// lease/heartbeat sweeper for the process lifetime.
		s.sessionEngine.Recover(ctx)
		go func() {
			t := time.NewTicker(2 * time.Second)
			defer t.Stop()
			for {
				select {
				case <-ctx.Done():
					return
				case <-t.C:
					s.sessionEngine.Sweep(ctx)
				}
			}
		}()
	}
	if s.sessionStore != nil && s.payment != nil {
		// Drive outstanding debits to a terminal accounting state. Until
		// this ran, a debit that failed after the work shipped was
		// simply lost — and the exchange reported as settled anyway.
		go s.runDebitRetry(ctx)
	}
	if s.sessionStore != nil {
		defer func() { _ = s.sessionStore.Close() }()
		// Idempotency-window retention for paid-job and top-up records.
		go func() {
			t := time.NewTicker(10 * time.Minute)
			defer t.Stop()
			for {
				select {
				case <-ctx.Done():
					return
				case <-t.C:
					cutoff := time.Now().Add(-jobRetention)
					if n, err := s.sessionStore.EvictJobs(cutoff); err == nil && n > 0 {
						log.Printf("evicted %d job idempotency records", n)
					}
					if n, err := s.sessionStore.EvictTopUps(cutoff); err == nil && n > 0 {
						log.Printf("evicted %d top-up idempotency records", n)
					}
				}
			}
		}()
	}
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
	if strings.TrimSpace(s.cfg.Listen.WorkerQUIC) != "" {
		go func() {
			if err := s.runWorkerQUIC(ctx, s.cfg.Listen.WorkerQUIC); err != nil {
				errCh <- fmt.Errorf("listen worker quic: %w", err)
				return
			}
			errCh <- nil
		}()
	}

	s.attachRunContext(ctx)
	if s.metadata != nil {
		go s.runMetadataRefresh(ctx, s.metadataRefreshInterval())
	}
	if s.poolSnapshot != nil {
		go s.poolSnapshot.Run(ctx)
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

func (s *Server) attachRunContext(ctx context.Context) {
	s.mu.Lock()
	s.runCtx = ctx
	healthMgr := s.health
	s.mu.Unlock()
	s.startHealthLoop(healthMgr)
}

func (s *Server) startHealthLoop(healthMgr *health.Manager) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.healthCancel != nil {
		s.healthCancel()
		s.healthCancel = nil
	}
	if s.runCtx == nil || healthMgr == nil {
		return
	}
	childCtx, cancel := context.WithCancel(s.runCtx)
	s.healthCancel = cancel
	go healthMgr.Run(childCtx)
}

func (s *Server) metadataRefreshInterval() time.Duration {
	if s.opts.MetadataRefreshInterval < 0 {
		return 0
	}
	if s.opts.MetadataRefreshInterval == 0 {
		return 5 * time.Minute
	}
	return s.opts.MetadataRefreshInterval
}

func (s *Server) runMetadataRefresh(ctx context.Context, interval time.Duration) {
	if interval <= 0 {
		return
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	client := &http.Client{Timeout: 2 * time.Second}
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			cfg := s.currentConfig()
			metadata := s.currentMetadata()
			if cfg == nil || metadata == nil {
				continue
			}
			refreshMetadataCatalog(ctx, client, cfg, metadata)
		}
	}
}

// parseKeyValidity reads the delegation window the operator published
// alongside the settlement key. Empty means unbounded — a deployment
// that has not published a delegation yet.
func parseKeyValidity(id config.Identity) (time.Time, time.Time, error) {
	parse := func(field, raw string) (time.Time, error) {
		raw = strings.TrimSpace(raw)
		if raw == "" {
			return time.Time{}, nil
		}
		t, err := time.Parse(time.RFC3339, raw)
		if err != nil {
			return time.Time{}, fmt.Errorf("%s: %w", field, err)
		}
		return t.UTC(), nil
	}
	notBefore, err := parse("settlement_key_not_before", id.SettlementKeyNotBefore)
	if err != nil {
		return time.Time{}, time.Time{}, err
	}
	expiresAt, err := parse("settlement_key_expires_at", id.SettlementKeyExpiresAt)
	if err != nil {
		return time.Time{}, time.Time{}, err
	}
	if !notBefore.IsZero() && !expiresAt.IsZero() && !expiresAt.After(notBefore) {
		return time.Time{}, time.Time{}, fmt.Errorf("settlement_key_expires_at must be after settlement_key_not_before")
	}
	return notBefore, expiresAt, nil
}

func describeValidity(notBefore, expiresAt time.Time) string {
	if notBefore.IsZero() && expiresAt.IsZero() {
		return "unbounded — publish a settlement_keys window and mirror it here"
	}
	from, until := "-inf", "+inf"
	if !notBefore.IsZero() {
		from = notBefore.Format(time.RFC3339)
	}
	if !expiresAt.IsZero() {
		until = expiresAt.Format(time.RFC3339)
	}
	return from + " .. " + until
}

// allocDebitSeq hands out the next debit sequence for a work_id.
//
// Durable when a state store is configured. Without one it falls back to
// an in-process counter, which is correct within a process and lost on
// restart — after which a work_id's sequence restarts and the payee
// deduplicates the first debits away as replays. That is the same
// caveat the in-process job idempotency carries, and the same reason
// session_store is required for spec conformance.
func (s *Server) allocDebitSeq(workID string) (uint64, error) {
	if s.sessionStore != nil {
		return s.sessionStore.NextDebitSeq(workID)
	}
	s.memDebitSeqMu.Lock()
	defer s.memDebitSeqMu.Unlock()
	if s.memDebitSeq == nil {
		s.memDebitSeq = map[string]uint64{}
	}
	s.memDebitSeq[workID]++
	return s.memDebitSeq[workID], nil
}
