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
