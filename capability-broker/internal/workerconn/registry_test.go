package workerconn

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/Cloud-SPE/livepeer-network-modules/capability-broker/internal/backend"
)

type stubForwarder struct {
	req backend.ForwardRequest
}

func (s *stubForwarder) Forward(_ context.Context, req backend.ForwardRequest) (*http.Response, error) {
	s.req = req
	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     make(http.Header),
		Body:       io.NopCloser(strings.NewReader("ok")),
	}, nil
}

func TestForwarderRoutesWorkerURLToRegistry(t *testing.T) {
	registry := NewRegistry()
	worker := &stubForwarder{}
	if err := registry.Register("backend-a", worker); err != nil {
		t.Fatalf("Register() error = %v", err)
	}
	f := NewForwarder(nil, registry)
	resp, err := f.Forward(context.Background(), backend.ForwardRequest{
		URL:    "worker://backend-a/v1/chat/completions",
		Method: http.MethodPost,
		Body:   strings.NewReader(`{}`),
	})
	if err != nil {
		t.Fatalf("Forward() error = %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if got, want := worker.req.URL, "http://worker.local/v1/chat/completions"; got != want {
		t.Fatalf("worker req URL = %q, want %q", got, want)
	}
}

func TestForwarderReturnsDisconnectedWorkerError(t *testing.T) {
	f := NewForwarder(nil, NewRegistry())
	_, err := f.Forward(context.Background(), backend.ForwardRequest{URL: "worker://missing/v1"})
	if err == nil || !strings.Contains(err.Error(), "not connected") {
		t.Fatalf("Forward() err = %v, want disconnected worker error", err)
	}
}

func TestForwarderDelegatesHTTPToFallback(t *testing.T) {
	fallback := &stubForwarder{}
	f := NewForwarder(fallback, NewRegistry())
	_, err := f.Forward(context.Background(), backend.ForwardRequest{URL: "http://backend/v1"})
	if err != nil {
		t.Fatalf("Forward() error = %v", err)
	}
	if got := fallback.req.URL; got != "http://backend/v1" {
		t.Fatalf("fallback URL = %q", got)
	}
}
