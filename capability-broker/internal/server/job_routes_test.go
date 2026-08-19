package server

import (
	"encoding/base64"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/Cloud-SPE/livepeer-network-modules/capability-broker/internal/config"
	"github.com/Cloud-SPE/livepeer-network-modules/capability-broker/internal/livepeerheader"
)

// End-to-end paid-job surface: transport negotiation, extractor claim,
// idempotent replay, request-id reuse rejection — over real HTTP with
// a fake backend and the mock payment daemon.

func newJobTestServer(t *testing.T, backendCalls *atomic.Int64) *httptest.Server {
	t.Helper()
	be := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		backendCalls.Add(1)
		if strings.Contains(r.Header.Get("Accept"), "text/event-stream") {
			w.Header().Set("Content-Type", "text/event-stream")
			fmt.Fprint(w, "data: {\"chunk\":1}\n\ndata: {\"usage\":{\"total_tokens\":21}}\n\ndata: [DONE]\n\n")
			return
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"choices":[{"text":"hi"}],"usage":{"total_tokens":42}}`)
	}))
	t.Cleanup(be.Close)

	dir := t.TempDir()
	keyPath := filepath.Join(dir, "seal.key")
	if err := os.WriteFile(keyPath, make([]byte, 32), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{
		Identity:      config.Identity{OrchEthAddress: "0x" + strings.Repeat("cd", 20)},
		PaymentDaemon: config.PaymentDaemon{Mock: true},
		SessionStore: config.SessionStore{
			Path:           filepath.Join(dir, "state.db"),
			SealingKeyFile: keyPath,
		},
		Capabilities: []config.Capability{{
			ID:         "openai:chat-completions",
			OfferingID: "default",
			Protocol:   "paid-job/v1",
			Job:        &config.JobCapability{Transports: []string{"unary", "stream"}},
			WorkUnit: config.WorkUnit{
				Name:      "tokens",
				Extractor: map[string]any{"type": "openai-usage"},
			},
			Health:  config.Health{InitialStatus: "ready"},
			Price:   config.Price{AmountWei: "1", PerUnits: 1},
			Backend: config.Backend{Transport: "http", URL: be.URL},
			Extra:   map[string]any{"openai": map[string]any{"model": "test-model"}, "provider": "vllm"},
		}},
	}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("config: %v", err)
	}
	s, err := New(cfg, Options{})
	if err != nil {
		t.Fatalf("server: %v", err)
	}
	t.Cleanup(func() {
		if s.sessionStore != nil {
			_ = s.sessionStore.Close()
		}
	})
	srv := httptest.NewServer(s.mux)
	t.Cleanup(srv.Close)
	return srv
}

func jobReq(t *testing.T, srv *httptest.Server, requestID, accept string) *http.Response {
	t.Helper()
	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/v1/job",
		strings.NewReader(`{"model":"test-model","messages":[]}`))
	req.Header.Set(livepeerheader.Capability, "openai:chat-completions")
	req.Header.Set(livepeerheader.Offering, "default")
	req.Header.Set(livepeerheader.Protocol, "paid-job/v1")
	req.Header.Set(livepeerheader.RequestID, requestID)
	req.Header.Set(livepeerheader.Payment, base64.StdEncoding.EncodeToString([]byte("stub-payment")))
	if accept != "" {
		req.Header.Set("Accept", accept)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	return resp
}

func TestJobSurfaceEndToEnd(t *testing.T) {
	var backendCalls atomic.Int64
	srv := newJobTestServer(t, &backendCalls)

	// --- unary happy path: extractor claims 42 tokens.
	resp := jobReq(t, srv, "job-req-1", "")
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("unary status %d: %s", resp.StatusCode, body)
	}
	if got := resp.Header.Get(livepeerheader.WorkUnits); got != "42" {
		t.Fatalf("Work-Units %q, want 42", got)
	}
	if got := resp.Header.Get(livepeerheader.WorkUnitName); got != "tokens" {
		t.Fatalf("Work-Unit %q, want tokens", got)
	}
	jobID := resp.Header.Get(livepeerheader.JobID)
	if jobID == "" {
		t.Fatal("missing Livepeer-Job-Id")
	}
	if !strings.Contains(string(body), "choices") {
		t.Fatalf("backend body not passed through: %s", body)
	}

	// --- idempotent replay: recorded outcome, no second backend call.
	before := backendCalls.Load()
	replay := jobReq(t, srv, "job-req-1", "")
	rbody, _ := io.ReadAll(replay.Body)
	replay.Body.Close()
	if replay.StatusCode != http.StatusOK {
		t.Fatalf("replay status %d", replay.StatusCode)
	}
	if got := replay.Header.Get(livepeerheader.WorkUnits); got != "42" {
		t.Fatalf("replay Work-Units %q, want 42", got)
	}
	if replay.Header.Get(livepeerheader.JobID) != jobID {
		t.Fatal("replay changed job id")
	}
	if !strings.Contains(string(rbody), `"replayed":true`) {
		t.Fatalf("replay body: %s", rbody)
	}
	if backendCalls.Load() != before {
		t.Fatal("replay re-executed the backend")
	}

	// --- request-id reuse with different content: rejected.
	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/v1/job",
		strings.NewReader(`{"different":"body-length-changes-fingerprint"}`))
	req.Header.Set(livepeerheader.Capability, "openai:chat-completions")
	req.Header.Set(livepeerheader.Offering, "default")
	req.Header.Set(livepeerheader.Protocol, "paid-job/v1")
	req.Header.Set(livepeerheader.RequestID, "job-req-1")
	req.Header.Set(livepeerheader.Payment, base64.StdEncoding.EncodeToString([]byte("other-payment")))
	reuse, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	reuse.Body.Close()
	if reuse.StatusCode != http.StatusBadRequest ||
		reuse.Header.Get(livepeerheader.Error) != livepeerheader.ErrRequestIDReuse {
		t.Fatalf("reuse: status %d error %q", reuse.StatusCode, reuse.Header.Get(livepeerheader.Error))
	}

	// --- undeclared transport refused pre-payment (multipart not declared).
	mreq, _ := http.NewRequest(http.MethodPost, srv.URL+"/v1/job", strings.NewReader("--x--"))
	mreq.Header.Set("Content-Type", "multipart/form-data; boundary=x")
	mreq.Header.Set(livepeerheader.Capability, "openai:chat-completions")
	mreq.Header.Set(livepeerheader.Offering, "default")
	mreq.Header.Set(livepeerheader.Protocol, "paid-job/v1")
	mreq.Header.Set(livepeerheader.RequestID, "job-req-mp")
	mreq.Header.Set(livepeerheader.Payment, base64.StdEncoding.EncodeToString([]byte("stub")))
	mresp, err := http.DefaultClient.Do(mreq)
	if err != nil {
		t.Fatal(err)
	}
	mresp.Body.Close()
	if mresp.StatusCode != http.StatusBadRequest ||
		mresp.Header.Get(livepeerheader.Error) != livepeerheader.ErrTransportUnsupported {
		t.Fatalf("transport refusal: %d %q", mresp.StatusCode, mresp.Header.Get(livepeerheader.Error))
	}

	// --- stream transport: body piped, claim in the trailer.
	sresp := jobReq(t, srv, "job-req-stream", "text/event-stream")
	sbody, _ := io.ReadAll(sresp.Body)
	sresp.Body.Close()
	if sresp.StatusCode != http.StatusOK {
		t.Fatalf("stream status %d", sresp.StatusCode)
	}
	if !strings.Contains(string(sbody), "[DONE]") {
		t.Fatalf("stream body: %s", sbody)
	}
	if got := sresp.Trailer.Get(livepeerheader.WorkUnits); got != "21" {
		t.Fatalf("stream trailer Work-Units %q, want 21 (trailers: %v)", got, sresp.Trailer)
	}
}
