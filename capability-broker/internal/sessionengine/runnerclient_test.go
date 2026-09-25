package sessionengine

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestHTTPRunnerClientRecognizesOnlyTypedCapacityRefusal(t *testing.T) {
	tests := []struct {
		name, body string
		status     int
		want       bool
		backoff    int
	}{
		{name: "typed", status: 429, body: `{"error":"capacity_reached"}`, want: true, backoff: 60},
		{name: "application 429", status: 429, body: `{"error":"rate_limited"}`},
		{name: "wrong status", status: 503, body: `{"error":"capacity_reached"}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Retry-After", "999")
				w.WriteHeader(tt.status)
				_, _ = w.Write([]byte(tt.body))
			}))
			defer ts.Close()
			c := &HTTPRunnerClient{BaseURL: ts.URL, Paths: RunnerPaths{Create: "/sessions"}}
			_, err := c.CreateSession(context.Background(), RunnerCreateRequest{})
			var capacity *RunnerCapacityError
			got := errors.As(err, &capacity)
			if got != tt.want {
				t.Fatalf("error = %v, typed capacity = %v", err, got)
			}
			if got && capacity.BackoffSeconds != tt.backoff {
				t.Fatalf("backoff = %d, want %d", capacity.BackoffSeconds, tt.backoff)
			}
		})
	}
}

func TestHTTPRunnerCreateResolutionRequiresAuthoritativeProof(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		code       int
		valid      bool
	}{
		{"created", `{"session_id":"sess_1","outcome":"created","runner_session_id":"rns_1"}`, 200, true},
		{"fenced", `{"session_id":"sess_1","outcome":"fenced"}`, 200, true},
		{"not-found", `{}`, 404, false},
		{"unknown", `{"session_id":"sess_1","outcome":"unknown"}`, 200, false},
		{"wrong-identity", `{"session_id":"sess_2","outcome":"fenced"}`, 200, false},
		{"no-runner-id", `{"session_id":"sess_1","outcome":"created"}`, 200, false},
		{"conflicting-proof", `{"session_id":"sess_1","outcome":"fenced","runner_session_id":"rns_1"}`, 200, false},
		{"malformed", `{`, 200, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != "POST" || r.URL.Path != "/reconcile" {
					t.Errorf("request=%s %s", r.Method, r.URL.Path)
				}
				w.WriteHeader(tc.code)
				w.Write([]byte(tc.body))
			}))
			defer ts.Close()
			c := &HTTPRunnerClient{BaseURL: ts.URL, Paths: RunnerPaths{Reconcile: "/reconcile"}}
			_, err := c.ReconcileSessionCreate(context.Background(), "sess_1")
			if (err == nil) != tc.valid {
				t.Fatalf("resolution error=%v", err)
			}
		})
	}
	c := &HTTPRunnerClient{}
	if _, err := c.ReconcileSessionCreate(context.Background(), "sess_1"); err == nil {
		t.Fatal("unsupported runner proved absence")
	}
}
