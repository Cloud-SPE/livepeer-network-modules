package runner

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

// fakeUpstream returns an httptest.Server that responds with a fixed
// SSE payload. The Content-Type header marks it as an event stream so
// the runner takes the streaming path.
func fakeUpstream(t *testing.T, payload string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, payload)
	}))
}

func newReadyRunner(t *testing.T, upstreamURL string) http.Handler {
	t.Helper()
	models := atomic.Value{}
	models.Store([]string{"test-model"})
	client := &http.Client{Transport: newTransport()}
	mux := http.NewServeMux()
	mux.HandleFunc(defaultEndpoint, func(w http.ResponseWriter, r *http.Request) {
		handleChatCompletions(w, r, client, upstreamURL, defaultMaxBodyBytes, "total_tokens", &models)
	})
	return mux
}

func TestHandler_StreamingEmitsTrailer(t *testing.T) {
	upstream := fakeUpstream(t, vllmStreamFixture)
	t.Cleanup(upstream.Close)

	handler := newReadyRunner(t, upstream.URL)

	body := strings.NewReader(`{"model":"test-model","stream":true,"messages":[{"role":"user","content":"hi"}]}`)
	req := httptest.NewRequest(http.MethodPost, defaultEndpoint, body)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d; want 200", rec.Code)
	}
	// Response body should include the SSE frames the upstream sent.
	if !bytes.Contains(rec.Body.Bytes(), []byte(`"content":"Hello"`)) {
		t.Fatalf("response body missing forwarded content: %s", rec.Body.String())
	}
	// Trailer should carry the work-units value.
	// httptest.ResponseRecorder exposes trailers on its Header() map
	// because they're declared via the Trailer response header.
	gotTrailer := rec.Header().Get(workUnitsTrailer)
	if gotTrailer != "42" {
		t.Fatalf("trailer %s = %q; want %q", workUnitsTrailer, gotTrailer, "42")
	}
}

func TestHandler_NonStreamingPassesThrough(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, `{"id":"x","usage":{"total_tokens":17}}`)
	}))
	t.Cleanup(upstream.Close)

	handler := newReadyRunner(t, upstream.URL)
	body := strings.NewReader(`{"model":"test-model","messages":[{"role":"user","content":"hi"}]}`)
	req := httptest.NewRequest(http.MethodPost, defaultEndpoint, body)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d; want 200", rec.Code)
	}
	if !bytes.Contains(rec.Body.Bytes(), []byte(`"total_tokens":17`)) {
		t.Fatalf("response body missing forwarded JSON: %s", rec.Body.String())
	}
	// No trailer should be set on non-streaming responses; the broker
	// uses openai-usage on the body for those.
	if got := rec.Header().Get(workUnitsTrailer); got != "" {
		t.Fatalf("non-streaming response should not set work-units trailer; got %q", got)
	}
}
