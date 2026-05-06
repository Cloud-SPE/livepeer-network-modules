// Package web is the secure-orch console's HTTP server. It binds
// 127.0.0.1 only — never a routable interface. Operators reach it
// via ssh -L from a LAN laptop.
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
	"strings"
	"sync"
	"time"

	"github.com/Cloud-SPE/livepeer-network-rewrite/secure-orch-console/internal/audit"
	"github.com/Cloud-SPE/livepeer-network-rewrite/secure-orch-console/internal/canonical"
	"github.com/Cloud-SPE/livepeer-network-rewrite/secure-orch-console/internal/config"
	"github.com/Cloud-SPE/livepeer-network-rewrite/secure-orch-console/internal/signing"
)

// Server bundles the console's HTTP surface with the deps the
// handlers need. Server only ever binds a loopback address; the
// constructor enforces this.
type Server struct {
	cfg          config.Config
	signer       signing.Signer
	audit        *audit.Log
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

// New builds a Server. The listen address is validated against the
// loopback gate — non-loopback binds are rejected.
func New(cfg config.Config, signer signing.Signer, log *audit.Log, logger *slog.Logger) (*Server, error) {
	if err := config.ValidateLoopbackAddr(cfg.Listen); err != nil {
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
	tmpls, err := loadTemplates()
	if err != nil {
		return nil, err
	}
	s := &Server{
		cfg:          cfg,
		signer:       signer,
		audit:        log,
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
	if err := assertLoopback(ln.Addr()); err != nil {
		_ = ln.Close()
		return nil, err
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
	s.mux.HandleFunc("GET /{$}", s.handleIndex)
	s.mux.HandleFunc("POST /candidate", s.handleCandidate)
	s.mux.HandleFunc("POST /discard", s.handleDiscard)
	s.mux.HandleFunc("POST /sign", s.handleSign)
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

func assertLoopback(addr net.Addr) error {
	tcp, ok := addr.(*net.TCPAddr)
	if !ok {
		return fmt.Errorf("web: unexpected listener address type %T", addr)
	}
	if !tcp.IP.IsLoopback() {
		return fmt.Errorf("web: listener bound non-loopback address %s (hard rule violation)", tcp.IP)
	}
	if strings.Contains(tcp.IP.String(), "0.0.0.0") {
		return fmt.Errorf("web: listener bound 0.0.0.0 (hard rule violation)")
	}
	return nil
}
