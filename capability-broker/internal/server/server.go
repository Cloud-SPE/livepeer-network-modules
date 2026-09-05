// Package server wires the HTTP server, route table, and middleware chain.
package server

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"log"
	"math/rand"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/Cloud-SPE/livepeer-network-modules/capability-broker/internal/backend"
	"github.com/Cloud-SPE/livepeer-network-modules/capability-broker/internal/certification"
	"github.com/Cloud-SPE/livepeer-network-modules/capability-broker/internal/config"
	"github.com/Cloud-SPE/livepeer-network-modules/capability-broker/internal/credentialstore"
	"github.com/Cloud-SPE/livepeer-network-modules/capability-broker/internal/extractors"
	"github.com/Cloud-SPE/livepeer-network-modules/capability-broker/internal/offers"
	"github.com/Cloud-SPE/livepeer-network-modules/capability-broker/internal/payment"
	"github.com/Cloud-SPE/livepeer-network-modules/capability-broker/internal/poolreport"
	"github.com/Cloud-SPE/livepeer-network-modules/capability-broker/internal/poolsnapshot"
	"github.com/Cloud-SPE/livepeer-network-modules/capability-broker/internal/receipts"
	"github.com/Cloud-SPE/livepeer-network-modules/capability-broker/internal/runners"
	"github.com/Cloud-SPE/livepeer-network-modules/capability-broker/internal/server/middleware"
	"github.com/Cloud-SPE/livepeer-network-modules/capability-broker/internal/sessionengine"
	"github.com/Cloud-SPE/livepeer-network-modules/capability-broker/internal/sessionstore"
	"github.com/Cloud-SPE/livepeer-network-modules/capability-broker/internal/settlement"
)

// Options aggregates non-host-config knobs the server takes at
// construction time. Per-request behavior knobs (e.g. plan 0015's
// interim-debit cadence) live here so they don't pollute the
// host-config.yaml grammar — operators set them via CLI flags.
type Options struct {
	ConfigPath string

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
	mux                  *http.ServeMux
	srv                  *http.Server
	metricsSrv           *http.Server
	payment              payment.Client
	extractors           *extractors.Registry
	backend              backend.Forwarder
	backendInFlight      map[string]int
	secrets              backend.SecretResolver
	receiptSink          receipts.Client
	poolReporter         poolreport.Client
	poolSnapshot         *poolsnapshot.Cache
	sessionStore         *sessionstore.Store
	credentialStore      *credentialstore.Store
	// runners is the registry of attached runners (plan 0043 item 7).
	runners *runners.Registry
	// offersEngine matches runners to offers, freezes shapes, and
	// decides eligibility (plan 0043 item 8).
	offersEngine *offers.Engine
	// certEngine executes certification runs (plan 0043 item 9).
	certEngine *certification.Engine
	// attachedHosts maps a host_id (credential-store enrollment) to the
	// connections it holds, so revoke = delete + kill (broker-admin
	// §5.3). Guarded by attachedMu.
	attachedMu    sync.Mutex
	attachedHosts map[string][]io.Closer
	sessionEngine *sessionengine.Engine
	sessionWS     *sessionWSHub
	jobIdem       jobIdemStore
	randIntn      func(int) int
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

	runnerRegistry := runners.New(0)

	var credStore *credentialstore.Store
	if cfg.CredentialStore.Enabled() {
		key, err := sessionstore.LoadKeyFile(cfg.CredentialStore.SealingKeyFile)
		if err != nil {
			return nil, fmt.Errorf("credential store: %w", err)
		}
		credStore, err = credentialstore.Open(cfg.CredentialStore.Path, key, credentialstore.Options{
			DefaultExpiry: time.Duration(cfg.CredentialStore.DefaultExpirySeconds) * time.Second,
			MaxExpiry:     time.Duration(cfg.CredentialStore.MaxExpirySeconds) * time.Second,
		})
		if err != nil {
			return nil, fmt.Errorf("credential store: %w", err)
		}
	}

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
		mux:              mux,
		srv:              srv,
		payment:          paymentClient,
		settlementSigner: settlementSigner,
		extractors:       defaultExtractors(),
		// runner:// (attached runners) → plain HTTP. The runner layer
		// claims its own scheme and delegates the rest.
		backend:         runnerForwarder{next: backend.NewHTTPClient(), registry: runnerRegistry},
		credentialStore: credStore,
		runners:         runnerRegistry,
		attachedHosts:   make(map[string][]io.Closer),
		backendInFlight: make(map[string]int),
		secrets:         secretResolver,
		receiptSink:     receiptSink,
		poolReporter:    poolReporter,
		poolSnapshot:    poolSnapshot,
		randIntn: func(n int) int {
			return rand.New(rand.NewSource(time.Now().UnixNano())).Intn(n)
		},
	}

	if len(cfg.Offers) > 0 && cfg.OffersStatePath == "" && cfg.OffersSource != config.OffersSourceAdmin {
		log.Printf("warning: offers[] configured without offers_state_path — frozen shapes will not survive a restart, " +
			"and a re-freeze from a different runner would be a silent manifest change")
	}
	certEngine := certification.New(s.runners, certification.Options{
		Extractors:  s.extractors,
		FixturesDir: cfg.CertificationFixturesDir,
		// A session runner under certification reports usage to a
		// callback under this base, the same way it reports to a paid
		// session's callback.
		CallbackBaseURL: cfg.ExternalBaseURL,
	})
	s.certEngine = certEngine
	offersEngine, err := offers.New(cfg, s.runners, cfg.OffersStatePath, certEngine)
	if err != nil {
		return nil, fmt.Errorf("offers engine: %w", err)
	}
	s.offersEngine = offersEngine
	// Terminal certification outcomes re-evaluate the pair — this is
	// where a first pass freezes an unfrozen offer.
	certEngine.Report = offersEngine.RecordCertification
	s.runners.OnChange = func(hostID string) { offersEngine.Rematch(hostID) }

	if err := s.initSessionEngine(); err != nil {
		return nil, fmt.Errorf("session engine: %w", err)
	}

	// Retention shorter than a payment envelope's life deletes the
	// broker's record of an exchange before a clearinghouse is able to
	// ask whether it happened — that question is only asked once the
	// envelope has expired, which is later than this.
	if r := s.jobRetention(); r < minEvidenceRetention {
		log.Printf("warning: session_store.job_retention is %s, shorter than the %s a payment "+
			"envelope can stay spendable; a consumer holding an encumbrance will find the "+
			"exchange record already evicted when it asks whether the envelope was ever used",
			r, minEvidenceRetention)
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

// adminTokenMatches reports whether an Authorization header carries the
// admin bearer token. A broker with no admin token configured accepts
// no token — never every token.
func (s *Server) adminTokenMatches(authz string) bool {
	s.mu.RLock()
	token := strings.TrimSpace(s.adminToken)
	s.mu.RUnlock()
	return token != "" && strings.TrimSpace(authz) == "Bearer "+token
}

func (s *Server) currentConfig() *config.Config {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.cfg
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
	if s.credentialStore != nil {
		defer func() { _ = s.credentialStore.Close() }()
	}
	go s.runRunnerEviction(ctx)
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
					cutoff := time.Now().Add(-s.jobRetention())
					if n, err := s.sessionStore.EvictJobs(cutoff); err == nil && n > 0 {
						log.Printf("evicted %d job idempotency records", n)
					}
					if n, err := s.sessionStore.EvictTopUps(cutoff); err == nil && n > 0 {
						log.Printf("evicted %d top-up idempotency records", n)
					}
					if n, err := s.sessionStore.EvictNonAdmissions(cutoff); err == nil && n > 0 {
						log.Printf("evicted %d non-admission records", n)
					}
					// Admission tombstones outlive the detailed records
					// they refer to, and pruning them advances the
					// horizon so the broker stops claiming it can answer
					// for a period it can no longer see.
					if n, err := s.sessionStore.EvictAdmissionTombstones(cutoff); err == nil && n > 0 {
						log.Printf("evicted %d admission tombstones; evidence horizon advanced", n)
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
	if addr := s.cfg.Listen.QUICAddr(); addr != "" {
		go func() {
			if err := s.runAttachQUIC(ctx, addr); err != nil {
				errCh <- fmt.Errorf("listen worker quic: %w", err)
				return
			}
			errCh <- nil
		}()
	}

	s.attachRunContext(ctx)
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
	s.mu.Unlock()
}

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
