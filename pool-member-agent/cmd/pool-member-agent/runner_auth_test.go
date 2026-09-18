package main

import (
	"github.com/Cloud-SPE/livepeer-network-modules/pool-member-agent/internal/attach"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
)

func TestLocalRunnerBearerOnlyOnBrokerTunnelAndNeverRedirected(t *testing.T) {
	var seen atomic.Value
	seen.Store("")
	var redirect atomic.Bool
	var leaked atomic.Int32
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { leaked.Add(1) }))
	defer target.Close()
	runner := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen.Store(r.Header.Get("Authorization"))
		if redirect.Load() {
			http.Redirect(w, r, target.URL, 302)
			return
		}
		w.WriteHeader(200)
	}))
	defer runner.Close()
	state := newRunnerState()
	state.set([]attach.Runner{{LocalID: "live", URL: runner.URL, LocalBearer: "local-only"}}, "revision")
	msg := tunnelMessage{Method: "POST", URL: "/v1/sessions", Headers: map[string][]string{LocalIDHeader: {"live"}, "Authorization": {"Bearer remote-input"}}}
	result := forwardTunnelRequest(t.Context(), state.routes(), msg)
	if result.StatusCode != 200 || seen.Load().(string) != "Bearer local-only" {
		t.Fatal(result, seen.Load())
	}
	// The public edge forwards only the public caller's grant, never local auth.
	edge := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "https://member.example/r/live/v1/sessions", nil)
	edgeHandler(state).ServeHTTP(edge, req)
	if edge.Code != 200 || seen.Load().(string) != "" {
		t.Fatal("public edge gained runner authority", seen.Load())
	}
	redirect.Store(true)
	result = forwardTunnelRequest(t.Context(), state.routes(), msg)
	if result.StatusCode != 302 || leaked.Load() != 0 {
		t.Fatal("local bearer followed redirect", result, leaked.Load())
	}
}
