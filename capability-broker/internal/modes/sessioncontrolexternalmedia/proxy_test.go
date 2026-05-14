package sessioncontrolexternalmedia

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Cloud-SPE/livepeer-network-rewrite/capability-broker/internal/livepeerheader"
)

// fakeBackend records every request the proxy forwards. Used to assert
// header stripping + path rewriting.
type fakeBackend struct {
	mu       *receivedRequest
	srv      *httptest.Server
	respBody string
	respCode int
}

type receivedRequest struct {
	path    string
	headers http.Header
}

func newFakeBackend(t *testing.T, respCode int, respBody string) *fakeBackend {
	t.Helper()
	fb := &fakeBackend{
		mu:       &receivedRequest{},
		respCode: respCode,
		respBody: respBody,
	}
	fb.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fb.mu.path = r.URL.Path
		fb.mu.headers = r.Header.Clone()
		w.WriteHeader(fb.respCode)
		_, _ = w.Write([]byte(fb.respBody))
	}))
	t.Cleanup(fb.srv.Close)
	return fb
}

// proxyMux builds an http.ServeMux registered with the driver's proxy
// handler at the expected path pattern.
func proxyMux(d *Driver) *http.ServeMux {
	m := http.NewServeMux()
	m.HandleFunc("/_scope/{session_id}/{path...}", d.ServeProxy)
	return m
}

func TestServeProxy_UnknownSessionReturns404(t *testing.T) {
	d := New(NewStore(), DefaultConfig())
	mux := proxyMux(d)
	req := httptest.NewRequest(http.MethodGet, "/_scope/sess_does_not_exist/api/v1/pipeline/status", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Result().StatusCode != http.StatusNotFound {
		t.Fatalf("got %d, want 404", w.Result().StatusCode)
	}
}

func TestServeProxy_ForwardsAndStripsLivepeerHeaders(t *testing.T) {
	be := newFakeBackend(t, http.StatusOK, `{"status":"loaded"}`)
	store := NewStore()
	d := New(store, DefaultConfig())

	rec := &SessionRecord{
		SessionID:        "sess_abc",
		BackendURL:       be.srv.URL,
		SessionStartPath: "/api/v1/session/start",
		SessionStopPath:  "/api/v1/session/stop",
		OpenedAt:         time.Now(),
		ExpiresAt:        time.Now().Add(time.Hour),
	}
	if err := store.Add(rec); err != nil {
		t.Fatalf("store.Add: %v", err)
	}

	mux := proxyMux(d)
	req := httptest.NewRequest(http.MethodGet, "/_scope/sess_abc/api/v1/pipeline/status", nil)
	req.Header.Set(livepeerheader.Capability, "daydream:scope:v1")
	req.Header.Set(livepeerheader.Offering, "default")
	req.Header.Set(livepeerheader.Payment, "<payment>")
	req.Header.Set(livepeerheader.Mode, "session-control-external-media@v0")
	req.Header.Set(livepeerheader.SpecVersion, "0.1")
	req.Header.Set("X-Custom-Header", "preserved")

	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Result().StatusCode != http.StatusOK {
		t.Fatalf("status: got %d, want 200", w.Result().StatusCode)
	}
	body, _ := io.ReadAll(w.Result().Body)
	if !strings.Contains(string(body), "loaded") {
		t.Fatalf("body: got %q, want loaded", string(body))
	}
	if be.mu.path != "/api/v1/pipeline/status" {
		t.Fatalf("backend path: got %q, want /api/v1/pipeline/status", be.mu.path)
	}
	for _, h := range []string{
		livepeerheader.Capability,
		livepeerheader.Offering,
		livepeerheader.Payment,
		livepeerheader.Mode,
		livepeerheader.SpecVersion,
	} {
		if v := be.mu.headers.Get(h); v != "" {
			t.Fatalf("backend received Livepeer header %q (=%q); should be stripped", h, v)
		}
	}
	if be.mu.headers.Get("X-Custom-Header") != "preserved" {
		t.Fatal("non-Livepeer header was incorrectly stripped")
	}
}

func TestServeProxy_StartsClockOnSessionStart(t *testing.T) {
	be := newFakeBackend(t, http.StatusOK, `{"started":true}`)
	store := NewStore()
	d := New(store, DefaultConfig())

	rec := &SessionRecord{
		SessionID:        "sess_abc",
		BackendURL:       be.srv.URL,
		SessionStartPath: "/api/v1/session/start",
		SessionStopPath:  "/api/v1/session/stop",
		OpenedAt:         time.Now(),
		ExpiresAt:        time.Now().Add(time.Hour),
	}
	if err := store.Add(rec); err != nil {
		t.Fatalf("store.Add: %v", err)
	}
	if !rec.StartedAt().IsZero() {
		t.Fatal("StartedAt should be zero before first proxy contact")
	}

	mux := proxyMux(d)
	req := httptest.NewRequest(http.MethodPost, "/_scope/sess_abc/api/v1/session/start", strings.NewReader(`{}`))
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Result().StatusCode != http.StatusOK {
		t.Fatalf("status: got %d, want 200", w.Result().StatusCode)
	}
	if rec.StartedAt().IsZero() {
		t.Fatal("StartedAt should be non-zero after session-start proxy call")
	}
}
