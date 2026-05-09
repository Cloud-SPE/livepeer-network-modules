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

	"github.com/Cloud-SPE/livepeer-network-rewrite/secure-orch-console/internal/audit"
	"github.com/Cloud-SPE/livepeer-network-rewrite/secure-orch-console/internal/canonical"
	"github.com/Cloud-SPE/livepeer-network-rewrite/secure-orch-console/internal/config"
	"github.com/Cloud-SPE/livepeer-network-rewrite/secure-orch-console/internal/protocol"
	"github.com/Cloud-SPE/livepeer-network-rewrite/secure-orch-console/internal/signing"
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

	mu        sync.Mutex
	candidate *stashedCandidate
}

type stashedCandidate struct {
	bytes      []byte
	loadedAt   time.Time
	canonHash  string
	sourceName string
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
		staticAssets: staticHandler(),
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
	s.mux.HandleFunc("POST /protocol/force-init", s.requireAuth(s.handleProtocolForceInit))
	s.mux.HandleFunc("POST /protocol/force-reward", s.requireAuth(s.handleProtocolForceReward))
	s.mux.HandleFunc("POST /protocol/set-service-uri", s.requireAuth(s.handleProtocolSetServiceURI))
	s.mux.HandleFunc("POST /protocol/set-ai-service-uri", s.requireAuth(s.handleProtocolSetAIServiceURI))
	s.mux.HandleFunc("GET /healthz", s.handleHealth)
	s.mux.Handle("GET /static/", http.StripPrefix("/static/", s.staticAssets))
	s.mux.HandleFunc("/", http.NotFound)
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	w.Write([]byte("ok\n"))
}

// CanonicalSHA256 is exposed so cmd/secure-orch-console can hash
// envelope bytes for boot-time logging without wrapping the canonical
// package directly.
func CanonicalSHA256(b []byte) string { return canonical.SHA256Hex(b) }
