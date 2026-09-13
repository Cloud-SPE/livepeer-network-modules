package server

import (
	"net/http"
	"testing"
	"time"
)

func TestRunnerCapacityRefusalIsNarrowAndBoundsBackoff(t *testing.T) {
	tests := []struct {
		name        string
		status      int
		body        string
		retryAfter  string
		wantOK      bool
		wantBackoff int
	}{
		{name: "typed default", status: 429, body: `{"error":"capacity_reached"}`, wantOK: true, wantBackoff: 5},
		{name: "typed bounded", status: 429, body: `{"error":"capacity_reached"}`, retryAfter: "300", wantOK: true, wantBackoff: 60},
		{name: "generic rate limit", status: 429, body: `{"error":"rate_limited"}`},
		{name: "wrong status", status: 503, body: `{"error":"capacity_reached"}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := make(http.Header)
			h.Set("Retry-After", tt.retryAfter)
			backoff, ok := runnerCapacityRefusal(tt.status, h, []byte(tt.body))
			if ok != tt.wantOK || backoff != tt.wantBackoff {
				t.Fatalf("got (%d,%v), want (%d,%v)", backoff, ok, tt.wantBackoff, tt.wantOK)
			}
		})
	}
}

func TestRunnerCapacityBackoffIsBackendLocalAndExpires(t *testing.T) {
	s := &Server{}
	s.markBackendCapacityRefused("host-a|live", 1)
	if !s.backendCapacityBackoff("host-a|live", time.Now()) {
		t.Fatal("refused backend was not placed in local backoff")
	}
	if s.backendCapacityBackoff("host-b|live", time.Now()) {
		t.Fatal("one GPU runner's refusal suppressed another backend")
	}
	if s.backendCapacityBackoff("host-a|live", time.Now().Add(2*time.Second)) {
		t.Fatal("expired capacity backoff remained active")
	}
}
