package sessioncontrolexternalmedia

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Cloud-SPE/livepeer-network-rewrite/capability-broker/internal/config"
	"github.com/Cloud-SPE/livepeer-network-rewrite/capability-broker/internal/modes"
)

func TestServeSessionOpen_HappyPath(t *testing.T) {
	store := NewStore()
	d := New(store, DefaultConfig())

	cap := &config.Capability{
		ID:              "daydream:scope:v1",
		OfferingID:      "default",
		InteractionMode: Mode,
		Backend: config.Backend{
			Transport: "http",
			URL:       "http://scope:8000",
		},
		Extra: map[string]any{
			"media_schema":       "scope-passthrough/v0",
			"session_start_path": "/api/v1/session/start",
			"session_stop_path":  "/api/v1/session/stop",
		},
	}

	req := httptest.NewRequest(http.MethodPost, "/v1/cap", strings.NewReader(`{}`))
	req.Host = "broker.example.com"
	w := httptest.NewRecorder()

	if err := d.Serve(context.Background(), modes.Params{
		Writer:     w,
		Request:    req,
		Capability: cap,
	}); err != nil {
		t.Fatalf("Serve: %v", err)
	}

	resp := w.Result()
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("status: got %d, want 202", resp.StatusCode)
	}

	var body sessionOpenResponse
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if !strings.HasPrefix(body.SessionID, "sess_") {
		t.Fatalf("session_id: got %q, want sess_<hex>", body.SessionID)
	}
	if !strings.HasPrefix(body.ControlURL, "ws://broker.example.com/v1/cap/"+body.SessionID+"/control") {
		t.Fatalf("control_url: got %q", body.ControlURL)
	}
	if body.Media.Schema != "scope-passthrough/v0" {
		t.Fatalf("media.schema: got %q", body.Media.Schema)
	}
	if !strings.HasPrefix(body.Media.ScopeURL, "http://broker.example.com/_scope/"+body.SessionID+"/") {
		t.Fatalf("media.scope_url: got %q", body.Media.ScopeURL)
	}
	if body.ExpiresAt == "" {
		t.Fatal("expires_at: empty")
	}

	if got := store.Get(body.SessionID); got == nil {
		t.Fatal("session not registered in store")
	}
}

func TestServeSessionOpen_RequiresBackendURL(t *testing.T) {
	store := NewStore()
	d := New(store, DefaultConfig())

	cap := &config.Capability{
		ID:              "daydream:scope:v1",
		OfferingID:      "default",
		InteractionMode: Mode,
		Backend:         config.Backend{Transport: "http"},
	}
	req := httptest.NewRequest(http.MethodPost, "/v1/cap", strings.NewReader(`{}`))
	req.Host = "broker.example.com"
	w := httptest.NewRecorder()

	_ = d.Serve(context.Background(), modes.Params{Writer: w, Request: req, Capability: cap})
	if w.Result().StatusCode != http.StatusInternalServerError {
		t.Fatalf("missing backend.url should 500; got %d", w.Result().StatusCode)
	}
}

func TestServeSessionOpen_RejectsNonPOST(t *testing.T) {
	d := New(NewStore(), DefaultConfig())
	cap := &config.Capability{
		ID:              "daydream:scope:v1",
		InteractionMode: Mode,
		Backend:         config.Backend{Transport: "http", URL: "http://scope:8000"},
	}
	req := httptest.NewRequest(http.MethodGet, "/v1/cap", nil)
	req.Host = "broker.example.com"
	w := httptest.NewRecorder()

	_ = d.Serve(context.Background(), modes.Params{Writer: w, Request: req, Capability: cap})
	if w.Result().StatusCode != http.StatusMethodNotAllowed {
		t.Fatalf("non-POST should 405; got %d", w.Result().StatusCode)
	}
}

func TestMode_IsCanonical(t *testing.T) {
	d := New(NewStore(), DefaultConfig())
	if d.Mode() != "session-control-external-media@v0" {
		t.Fatalf("Mode(): got %q", d.Mode())
	}
}

func TestDeriveControlURL_HonorsExternalScheme(t *testing.T) {
	tests := []struct {
		name string
		tls  bool
		xfp  string
		want string
	}{
		{
			name: "plain http",
			want: "ws://broker.example.com/v1/cap/sess_x/control",
		},
		{
			name: "direct tls",
			tls:  true,
			want: "wss://broker.example.com/v1/cap/sess_x/control",
		},
		{
			name: "tls terminated proxy",
			xfp:  "https",
			want: "wss://broker.example.com/v1/cap/sess_x/control",
		},
		{
			name: "forwarded proto list",
			xfp:  "https, http",
			want: "wss://broker.example.com/v1/cap/sess_x/control",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/v1/cap", nil)
			req.Host = "broker.example.com"
			if tc.tls {
				req.TLS = &tls.ConnectionState{}
			}
			if tc.xfp != "" {
				req.Header.Set("X-Forwarded-Proto", tc.xfp)
			}

			got, err := deriveControlURL(req, "sess_x")
			if err != nil {
				t.Fatalf("deriveControlURL: %v", err)
			}
			if got != tc.want {
				t.Fatalf("control_url: got %q want %q", got, tc.want)
			}
		})
	}
}

func TestDeriveScopeURL_HonorsExternalScheme(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/v1/cap", nil)
	req.Host = "broker.example.com"
	req.Header.Set("X-Forwarded-Proto", "https")

	got, err := deriveScopeURL(req, "sess_x")
	if err != nil {
		t.Fatalf("deriveScopeURL: %v", err)
	}
	want := "https://broker.example.com/_scope/sess_x/"
	if got != want {
		t.Fatalf("scope_url: got %q want %q", got, want)
	}
}
