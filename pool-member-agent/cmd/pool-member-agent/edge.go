package main

import (
	"context"
	"crypto/tls"
	"errors"
	"log"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/Cloud-SPE/livepeer-network-modules/pool-member-agent/internal/attach"
)

// The member's public edge (plan 0046 §2).
//
// Every paid-session data plane is external: the caller connects to the
// runner directly at the url the descriptor publishes. A runner in a
// compose network has no address a caller can reach, and giving every
// runner author TLS to solve would put the certificate in as many
// places as there are images. So the agent owns it: one listener, one
// certificate, routing /r/<local_id>/<rest> to http://<local_id>:8080/<rest>
// — the same address the agent fetches contracts from. Runners speak
// plain HTTP and WebSocket inside the host; ReverseProxy carries the
// upgrade.
//
// The edge runs only when LIVEPEER_PUBLIC_URL is set, because that is
// the host telling the pool it is reachable. A host that says so and
// cannot serve — no certificate, port not forwarded — fails the
// certification reach dial by name, which is the design: the pool
// proves reachability rather than trusting the claim.

const edgePrefix = "/r/"

// edgeHandler routes by local id against the live runner set.
func edgeHandler(state *runnerState) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})
	mux.HandleFunc(edgePrefix, func(w http.ResponseWriter, r *http.Request) {
		localID, rest, _ := strings.Cut(strings.TrimPrefix(r.URL.Path, edgePrefix), "/")
		runner, ok := runnerByLocalID(state, localID)
		if !ok {
			http.Error(w, "no such runner", http.StatusNotFound)
			return
		}
		target, err := url.Parse(runner.URL)
		if err != nil {
			http.Error(w, "runner url", http.StatusBadGateway)
			return
		}
		proxy := httputil.NewSingleHostReverseProxy(target)
		director := proxy.Director
		proxy.Director = func(req *http.Request) {
			director(req)
			req.URL.Path = "/" + rest
			req.URL.RawPath = ""
			req.Host = target.Host
		}
		proxy.ErrorHandler = func(w http.ResponseWriter, _ *http.Request, err error) {
			log.Printf("edge: %s -> %s: %v", r.URL.Path, runner.URL, err)
			http.Error(w, "runner unreachable", http.StatusBadGateway)
		}
		proxy.ServeHTTP(w, r)
	})
	return mux
}

func runnerByLocalID(state *runnerState, localID string) (attach.Runner, bool) {
	state.mu.RLock()
	defer state.mu.RUnlock()
	for _, r := range state.runners {
		if r.LocalID == localID {
			return r, true
		}
	}
	return attach.Runner{}, false
}

// startEdge serves the edge until ctx ends. It returns an error only
// for a misconfiguration the operator has to fix; a host that has not
// declared itself public simply has no edge.
func startEdge(ctx context.Context, state *runnerState) error {
	if strings.TrimSpace(os.Getenv("LIVEPEER_PUBLIC_URL")) == "" {
		return nil
	}
	listen := envOr("LIVEPEER_EDGE_LISTEN", ":8443")
	certFile := envOr("LIVEPEER_EDGE_TLS_CERT", "/etc/livepeer/edge/tls.crt")
	keyFile := envOr("LIVEPEER_EDGE_TLS_KEY", "/etc/livepeer/edge/tls.key")
	cert, err := tls.LoadX509KeyPair(certFile, keyFile)
	if err != nil {
		// Loud and specific: the host claims to be public and cannot
		// be. Attaching continues — job work is unaffected — and the
		// certification reach dial names this host when it fails.
		return errors.New("PUBLIC EDGE CANNOT START: LIVEPEER_PUBLIC_URL is set but the certificate is unusable (" +
			certFile + ", " + keyFile + "): " + err.Error())
	}
	srv := &http.Server{
		Addr:              listen,
		Handler:           edgeHandler(state),
		TLSConfig:         &tls.Config{Certificates: []tls.Certificate{cert}, MinVersion: tls.VersionTLS12},
		ReadHeaderTimeout: 10 * time.Second,
	}
	go func() {
		<-ctx.Done()
		shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutdown)
	}()
	if err := startRTMPSEdge(ctx, state, cert); err != nil {
		log.Print(err)
	}
	log.Printf("public edge listening on %s for %s", listen, os.Getenv("LIVEPEER_PUBLIC_URL"))
	go func() {
		if err := srv.ListenAndServeTLS("", ""); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Printf("PUBLIC EDGE STOPPED: %v", err)
		}
	}()
	return nil
}
