// Package web is the secure-orch console's HTTP server. It binds
// 127.0.0.1 only — never a routable interface. Operators reach it
// via ssh -L from a LAN laptop.
//
// Today the server stubs the route surface (/, /diff, /sign, /audit)
// with placeholder JSON responses. Commit 5 lands the web UI
// (HTML/CSS/JS embedded via embed.FS) on top of these handlers.
package web

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/Cloud-SPE/livepeer-network-rewrite/secure-orch-console/internal/audit"
	"github.com/Cloud-SPE/livepeer-network-rewrite/secure-orch-console/internal/canonical"
	"github.com/Cloud-SPE/livepeer-network-rewrite/secure-orch-console/internal/config"
	"github.com/Cloud-SPE/livepeer-network-rewrite/secure-orch-console/internal/diff"
	"github.com/Cloud-SPE/livepeer-network-rewrite/secure-orch-console/internal/inbox"
	"github.com/Cloud-SPE/livepeer-network-rewrite/secure-orch-console/internal/outbox"
	"github.com/Cloud-SPE/livepeer-network-rewrite/secure-orch-console/internal/signing"
)

// Server bundles the console's HTTP surface with the deps the
// handlers need. Server only ever binds a loopback address; the
// constructor enforces this.
type Server struct {
	cfg      config.Config
	signer   signing.Signer
	inbox    *inbox.Inbox
	outbox   *outbox.Outbox
	audit    *audit.Log
	logger   *slog.Logger
	mux      *http.ServeMux
	listener net.Listener
	httpSrv  *http.Server
}

// New builds a Server. The listen address is validated against the
// loopback gate — non-loopback binds are rejected.
func New(cfg config.Config, signer signing.Signer, in *inbox.Inbox, out *outbox.Outbox, log *audit.Log, logger *slog.Logger) (*Server, error) {
	if err := config.ValidateLoopbackAddr(cfg.Listen); err != nil {
		return nil, err
	}
	if signer == nil {
		return nil, errors.New("web: signer is required")
	}
	if in == nil || out == nil || log == nil {
		return nil, errors.New("web: inbox/outbox/audit are required")
	}
	if logger == nil {
		logger = slog.Default()
	}
	s := &Server{
		cfg:    cfg,
		signer: signer,
		inbox:  in,
		outbox: out,
		audit:  log,
		logger: logger,
		mux:    http.NewServeMux(),
	}
	s.routes()
	return s, nil
}

// Listen binds the server's TCP listener. ListenAndServe is split into
// Listen + Serve so a startup test can verify the bound address
// before the server starts accepting requests.
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
	s.mux.HandleFunc("GET /", s.handleIndex)
	s.mux.HandleFunc("GET /diff", s.handleDiff)
	s.mux.HandleFunc("POST /sign", s.handleSign)
	s.mux.HandleFunc("GET /audit", s.handleAudit)
	s.mux.HandleFunc("GET /healthz", s.handleHealth)
}

func (s *Server) handleIndex(w http.ResponseWriter, r *http.Request) {
	candidates, err := s.inbox.List()
	if err != nil {
		s.fail(w, r, http.StatusInternalServerError, "list inbox", err)
		return
	}
	json.NewEncoder(w).Encode(map[string]any{
		"signer_address": s.signer.Address().String(),
		"inbox_dir":      s.inbox.Dir(),
		"outbox_dir":     s.outbox.Dir(),
		"candidates":     candidates,
		"note":           "stub response; web UI lands in commit 5",
	})
}

func (s *Server) handleDiff(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Query().Get("path")
	if path == "" {
		http.Error(w, "missing ?path=", http.StatusBadRequest)
		return
	}
	cand, err := s.inbox.Load(path)
	if err != nil {
		s.fail(w, r, http.StatusBadRequest, "load candidate", err)
		return
	}
	last, err := s.outbox.LoadLastSigned()
	if err != nil {
		s.fail(w, r, http.StatusInternalServerError, "load last-signed", err)
		return
	}
	res, err := diff.Compute(last, cand.Bytes)
	if err != nil {
		s.fail(w, r, http.StatusBadRequest, "compute diff", err)
		return
	}
	if appendErr := s.audit.Append(audit.Event{
		Kind:       audit.KindViewDiff,
		EthAddress: s.signer.Address().String(),
		Note:       cand.Path,
	}); appendErr != nil {
		s.logger.Warn("audit append failed", "err", appendErr)
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(res)
}

func (s *Server) handleSign(w http.ResponseWriter, r *http.Request) {
	// Sign happens here in commit 5 once the confirm gesture lands.
	// This stub returns 501 so any premature client integration fails
	// loud rather than silent.
	if appendErr := s.audit.Append(audit.Event{
		Kind:       audit.KindAbort,
		EthAddress: s.signer.Address().String(),
		Note:       "POST /sign called before commit 5 wiring",
	}); appendErr != nil {
		s.logger.Warn("audit append failed", "err", appendErr)
	}
	http.Error(w, "sign endpoint is stubbed; lands in commit 5", http.StatusNotImplemented)
}

func (s *Server) handleAudit(w http.ResponseWriter, r *http.Request) {
	json.NewEncoder(w).Encode(map[string]any{
		"audit_log_path": s.audit.Path(),
		"note":           "tail the file directly; web reader lands in commit 5",
	})
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	w.Write([]byte("ok\n"))
}

func (s *Server) fail(w http.ResponseWriter, r *http.Request, code int, what string, err error) {
	s.logger.Warn("handler failed", "what", what, "method", r.Method, "path", r.URL.Path, "err", err)
	http.Error(w, fmt.Sprintf("%s: %s", what, err), code)
}

// CanonicalSHA256 is exposed so cmd/secure-orch-console can hash
// envelope bytes for boot-time logging without wrapping the canonical
// package directly.
func CanonicalSHA256(b []byte) string { return canonical.SHA256Hex(b) }

// assertLoopback is a defense-in-depth check after net.Listen returns:
// even if config.ValidateLoopbackAddr accepted the input, confirm the
// kernel-reported bound address is loopback.
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
