package server

import (
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/Cloud-SPE/livepeer-network-modules/capability-broker/internal/certification"
)

// Run-scoped fixture source and output sink (plan 0045 §7).
//
// Both answer only for a scope the certification engine currently holds
// open, and both give the same refusal for an unknown scope and an
// unknown ref: a prober must not be able to enumerate which
// certification runs exist, and a 404 that differs by cause would tell
// it. The scope id is the capability — 128 random bits in a URL handed
// to one runner for one run — which is why neither route wants a token.

func (s *Server) handleCertificationFixture(w http.ResponseWriter, r *http.Request) {
	if s.certEngine == nil {
		http.NotFound(w, r)
		return
	}
	scope := strings.TrimSpace(r.PathValue("scope"))
	ref := strings.TrimSpace(r.PathValue("ref"))
	data, ct, ok := s.certEngine.FixtureFor(scope, ref)
	if !ok {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", ct)
	w.Header().Set("Content-Length", strconv.Itoa(len(data)))
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(data)
}

func (s *Server) handleCertificationSink(w http.ResponseWriter, r *http.Request) {
	if s.certEngine == nil {
		http.NotFound(w, r)
		return
	}
	scope := strings.TrimSpace(r.PathValue("scope"))
	// Read (and discard) before deciding, so a runner streaming a large
	// body to an unknown scope is not told mid-stream — the refusal is
	// the same 404 either way, just after the body.
	n, err := io.Copy(io.Discard, io.LimitReader(r.Body, certification.MaxSinkBytes+1))
	if err != nil {
		http.Error(w, "read upload: "+err.Error(), http.StatusBadRequest)
		return
	}
	if n > certification.MaxSinkBytes {
		http.Error(w, "upload exceeds the probe sink's limit", http.StatusRequestEntityTooLarge)
		return
	}
	if !s.certEngine.SinkAccept(scope, n) {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(`{"accepted":true,"bytes":` + strconv.FormatInt(n, 10) + `}`))
}
