// Package web is the secure-orch console's HTTP server.
//
// The server hosts the candidate-upload form, renders the structural
// diff against last-signed.json, runs the tap-to-sign confirm gesture,
// and returns the signed envelope as a download attachment. There is
// no inbox / outbox spool — manifest transport is HTTP-only.
package web

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/Cloud-SPE/livepeer-network-modules/secure-orch-console/internal/agent"
	"github.com/Cloud-SPE/livepeer-network-modules/secure-orch-console/internal/audit"
	"github.com/Cloud-SPE/livepeer-network-modules/secure-orch-console/internal/canonical"
	"github.com/Cloud-SPE/livepeer-network-modules/secure-orch-console/internal/config"
	"github.com/Cloud-SPE/livepeer-network-modules/secure-orch-console/internal/protocol"
	"github.com/Cloud-SPE/livepeer-network-modules/secure-orch-console/internal/signing"
)

// Server bundles the console's HTTP surface with the deps the
// handlers need.
type Server struct {
	cfg          config.Config
	signer       signing.Signer
	audit        *audit.Log
	auth         *authManager
	protocol     *protocol.Client
	logger       *slog.Logger
	mux          *http.ServeMux
	listener     net.Listener
	httpSrv      *http.Server
	maxUpload    int64
	templates    *templateSet
	staticAssets http.Handler

	// held is the plan 0042 agent held queue; nil when the console
	// runs without an agent held dir configured.
	held *agent.HeldQueue
	// metricsHandler serves the agent's Prometheus exposition on the
	// loopback listener (constraint #1 rules out a separate metrics
	// listener); nil when no agent runs.
	metricsHandler http.Handler

	mu        sync.Mutex
	candidate *stashedCandidate
	agent     AgentControls
}

type stashedCandidate struct {
	bytes      []byte
	loadedAt   time.Time
	canonHash  string
	sourceName string
	// heldETag is non-empty when the candidate was loaded from the
	// agent's held queue; signing it is an operator approval — the
	// held slot clears and the agent pushes (no manual download).
	heldETag string
}

// New builds a Server.
func New(cfg config.Config, signer signing.Signer, log *audit.Log, logger *slog.Logger) (*Server, error) {
	if err := config.ValidateListenAddr(cfg.Listen); err != nil {
		return nil, err
	}
	if signer == nil {
		return nil, errors.New("web: signer is required")
	}
	if log == nil {
		return nil, errors.New("web: audit log is required")
	}
	if logger == nil {
		logger = slog.Default()
	}
	var protocolClient *protocol.Client
	if cfg.ProtocolSocket != "" {
		client, err := protocol.Dial(context.Background(), cfg.ProtocolSocket)
		if err != nil {
			return nil, err
		}
		protocolClient = client
	}
	tmpls, err := loadTemplates()
	if err != nil {
		return nil, err
	}
	s := &Server{
		cfg:          cfg,
		signer:       signer,
		audit:        log,
		auth:         newAuthManager(cfg.AdminTokens),
		protocol:     protocolClient,
		logger:       logger,
		mux:          http.NewServeMux(),
		maxUpload:    8 << 20,
		templates:    tmpls,
		staticAssets: staticHandler(cfg.Version),
	}
	if cfg.AgentHeldDir != "" {
		s.held = &agent.HeldQueue{Dir: cfg.AgentHeldDir}
	}
	s.routes()
	return s, nil
}

// Listen binds the server's TCP listener.
func (s *Server) Listen() (net.Addr, error) {
	if s.listener != nil {
		return s.listener.Addr(), nil
	}
	ln, err := net.Listen("tcp", s.cfg.Listen)
	if err != nil {
		return nil, fmt.Errorf("web: listen %s: %w", s.cfg.Listen, err)
	}
	s.listener = ln
	s.httpSrv = &http.Server{
		Handler:           s.mux,
		ReadHeaderTimeout: 5 * time.Second,
	}
	return ln.Addr(), nil
}

// Serve runs the HTTP server until ctx is canceled or Shutdown is
// called.
func (s *Server) Serve(ctx context.Context) error {
	if s.listener == nil {
		if _, err := s.Listen(); err != nil {
			return err
		}
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

// Addr returns the bound address; empty string before Listen.
func (s *Server) Addr() string {
	if s.listener == nil {
		return ""
	}
	return s.listener.Addr().String()
}

func (s *Server) routes() {
	s.mux.HandleFunc("GET /{$}", s.requireAuth(s.handleIndex))
	s.mux.HandleFunc("GET /overview", s.requireAuth(s.handleOverviewPage))
	s.mux.HandleFunc("GET /protocol-status", s.requireAuth(s.handleProtocolStatusPage))
	s.mux.HandleFunc("GET /protocol-actions", s.requireAuth(s.handleProtocolActionsPage))
	s.mux.HandleFunc("GET /manifests", s.requireAuth(s.handleManifestsPage))
	s.mux.HandleFunc("GET /audit", s.requireAuth(s.handleAuditPage))
	s.mux.HandleFunc("GET /login", s.handleLoginPage)
	s.mux.HandleFunc("POST /login", s.handleLoginSubmit)
	s.mux.HandleFunc("POST /logout", s.requireAuth(s.handleLogout))
	s.mux.HandleFunc("POST /candidate", s.requireAuth(s.handleCandidate))
	s.mux.HandleFunc("POST /discard", s.requireAuth(s.handleDiscard))
	s.mux.HandleFunc("POST /sign", s.requireAuth(s.handleSign))
	s.mux.HandleFunc("POST /held/load", s.requireAuth(s.handleHeldLoad))
	s.mux.HandleFunc("POST /agent/rate-limit/clear", s.requireAuth(s.handleClearRateLimit))
	s.mux.HandleFunc("POST /protocol/force-init", s.requireAuth(s.handleProtocolForceInit))
	s.mux.HandleFunc("POST /protocol/force-reward", s.requireAuth(s.handleProtocolForceReward))
	s.mux.HandleFunc("POST /protocol/set-service-uri", s.requireAuth(s.handleProtocolSetServiceURI))
	s.mux.HandleFunc("POST /protocol/set-ai-service-uri", s.requireAuth(s.handleProtocolSetAIServiceURI))
	s.mux.HandleFunc("POST /protocol/set-transcoder", s.requireAuth(s.handleProtocolSetTranscoder))
	s.mux.HandleFunc("POST /protocol/force-transfer-bond", s.requireAuth(s.handleProtocolForceTransferBond))
	s.mux.HandleFunc("POST /protocol/force-withdraw-fees", s.requireAuth(s.handleProtocolForceWithdrawFees))
	s.mux.HandleFunc("POST /protocol/cast-vote", s.requireAuth(s.handleProtocolCastVote))
	s.mux.HandleFunc("POST /protocol/set-config", s.requireAuth(s.handleProtocolSetConfig))
	s.mux.HandleFunc("GET /healthz", s.handleHealth)
	s.mux.HandleFunc("GET /metrics", s.handleMetrics)
	s.mux.Handle("GET /static/", http.StripPrefix("/static/", s.staticAssets))
	s.mux.HandleFunc("/", http.NotFound)
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	w.Write([]byte("ok\n"))
}

// SetMetricsHandler installs the agent's metrics exposition. Call
// before Listen.
func (s *Server) SetMetricsHandler(h http.Handler) { s.metricsHandler = h }

// AgentControls is the slice of the in-process agent the console can
// act on. Nil when the console runs without an agent, in which case the
// gestures are simply not offered.
type AgentControls interface {
	RateLimitPaused() bool
	ClearRateLimit(actor string) bool
}

// SetAgentControls wires the operator gestures the agent exposes. Call
// before Listen.
func (s *Server) SetAgentControls(c AgentControls) { s.agent = c }

func (s *Server) handleMetrics(w http.ResponseWriter, r *http.Request) {
	if s.metricsHandler == nil {
		http.NotFound(w, r)
		return
	}
	s.metricsHandler.ServeHTTP(w, r)
}

// CanonicalSHA256 is exposed so cmd/secure-orch-console can hash
// envelope bytes for boot-time logging without wrapping the canonical
// package directly.
func CanonicalSHA256(b []byte) string { return canonical.SHA256Hex(b) }
