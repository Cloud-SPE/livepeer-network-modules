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

func fakeUpstream(t *testing.T, body string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, body)
	}))
}

func newReadyRunner(t *testing.T, upstreamURL string) http.Handler {
	t.Helper()
	models := atomic.Value{}
	models.Store([]string{"bge-large-en-v1.5"})
	client := &http.Client{Transport: newTransport()}
	mux := http.NewServeMux()
	mux.HandleFunc(defaultEndpoint, func(w http.ResponseWriter, r *http.Request) {
		handleEmbeddings(w, r, client, upstreamURL, defaultMaxBodyBytes, "total_tokens", &models)
	})
	return mux
}

func TestHandler_EmitsWorkUnitsHeader(t *testing.T) {
	upstream := fakeUpstream(t, vllmEmbeddingsFixture)
	t.Cleanup(upstream.Close)

	handler := newReadyRunner(t, upstream.URL)

	body := strings.NewReader(`{"model":"bge-large-en-v1.5","input":"hello world"}`)
	req := httptest.NewRequest(http.MethodPost, defaultEndpoint, body)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d; want 200", rec.Code)
	}
	if got := rec.Header().Get(workUnitsHeader); got != "17" {
		t.Fatalf("%s = %q; want 17", workUnitsHeader, got)
	}
	if !bytes.Contains(rec.Body.Bytes(), []byte(`"embedding"`)) {
		t.Fatalf("response body should forward upstream embedding: %s", rec.Body.String())
	}
}

func TestHandler_OllamaUpstream(t *testing.T) {
	upstream := fakeUpstream(t, ollamaEmbeddingsFixture)
	t.Cleanup(upstream.Close)

	handler := newReadyRunner(t, upstream.URL)
	body := strings.NewReader(`{"model":"nomic-embed-text","input":"hello"}`)
	req := httptest.NewRequest(http.MethodPost, defaultEndpoint, body)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d; want 200", rec.Code)
	}
	if got := rec.Header().Get(workUnitsHeader); got != "8" {
		t.Fatalf("ollama %s = %q; want 8", workUnitsHeader, got)
	}
}

func TestHandler_MissingUsageOmitsHeader(t *testing.T) {
	upstream := fakeUpstream(t, `{"object":"list","data":[]}`)
	t.Cleanup(upstream.Close)

	handler := newReadyRunner(t, upstream.URL)
	body := strings.NewReader(`{"model":"x","input":"empty"}`)
	req := httptest.NewRequest(http.MethodPost, defaultEndpoint, body)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	// No usage in response → don't emit a header (avoid claiming 0).
	if got := rec.Header().Get(workUnitsHeader); got != "" {
		t.Fatalf("%s should be absent when upstream has no usage; got %q", workUnitsHeader, got)
	}
}

func TestHandler_RejectsNonPost(t *testing.T) {
	upstream := fakeUpstream(t, vllmEmbeddingsFixture)
	t.Cleanup(upstream.Close)

	handler := newReadyRunner(t, upstream.URL)
	req := httptest.NewRequest(http.MethodGet, defaultEndpoint, nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d; want 405", rec.Code)
	}
}

func TestHandler_StripsLivepeerHeaders(t *testing.T) {
	gotHeaders := http.Header{}
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		for k, vv := range r.Header {
			gotHeaders[k] = vv
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, vllmEmbeddingsFixture)
	}))
	t.Cleanup(upstream.Close)

	handler := newReadyRunner(t, upstream.URL)
	body := strings.NewReader(`{"model":"x","input":"hi"}`)
	req := httptest.NewRequest(http.MethodPost, defaultEndpoint, body)
	req.Header.Set("Livepeer", "eyJjYXBhYmlsaXR5IjoidGVzdCJ9")
	req.Header.Set("Authorization", "Bearer secret")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if v := gotHeaders.Get("Livepeer"); v != "" {
		t.Fatalf("Livepeer header should be stripped; got %q", v)
	}
	if v := gotHeaders.Get("Authorization"); v != "" {
		t.Fatalf("Authorization header should be stripped; got %q", v)
	}
}
