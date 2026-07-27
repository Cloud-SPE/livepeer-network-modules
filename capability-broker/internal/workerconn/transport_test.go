package workerconn

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/Cloud-SPE/livepeer-network-modules/capability-broker/internal/backend"
)

type probeStubForwarder struct {
	gotURL     string
	gotMethod  string
	gotNilBody bool
}

func (s *probeStubForwarder) Forward(_ context.Context, req backend.ForwardRequest) (*http.Response, error) {
	s.gotURL = req.URL
	s.gotMethod = req.Method
	s.gotNilBody = req.Body == nil
	return &http.Response{
		StatusCode: http.StatusOK,
		Body:       io.NopCloser(strings.NewReader(`{"ok":true}`)),
		Header:     make(http.Header),
	}, nil
}

type probeStubRoundTripper struct{ called bool }

func (s *probeStubRoundTripper) RoundTrip(*http.Request) (*http.Response, error) {
	s.called = true
	return &http.Response{
		StatusCode: http.StatusTeapot,
		Body:       io.NopCloser(strings.NewReader("")),
		Header:     make(http.Header),
	}, nil
}

// A worker:// probe must be forwarded over the connected session, not sent to
// the base transport (which would fail with "unsupported protocol scheme").
func TestHTTPTransportRoutesWorkerSchemeToRegistry(t *testing.T) {
	reg := NewRegistry()
	fwd := &probeStubForwarder{}
	if err := reg.Register("b1", fwd); err != nil {
		t.Fatalf("register: %v", err)
	}
	base := &probeStubRoundTripper{}
	client := &http.Client{Transport: reg.HTTPTransport(base)}

	resp, err := client.Get("worker://b1/v1/models")
	if err != nil {
		t.Fatalf("worker probe error: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if fwd.gotMethod != http.MethodGet {
		t.Fatalf("forwarder method = %q, want GET", fwd.gotMethod)
	}
	if fwd.gotNilBody {
		t.Fatal("forwarder received a nil Body for a bodyless GET probe (would panic in SessionForwarder)")
	}
	if base.called {
		t.Fatal("base transport must not be used for the worker:// scheme")
	}
}

// A worker:// probe to a backend that is not connected must surface an error
// (so the prober marks it unreachable) rather than silently passing.
func TestHTTPTransportWorkerSchemeUnconnectedErrors(t *testing.T) {
	reg := NewRegistry()
	client := &http.Client{Transport: reg.HTTPTransport(nil)}
	if _, err := client.Get("worker://absent/v1/models"); err == nil {
		t.Fatal("expected error probing an unconnected worker backend")
	}
}

// Non-worker schemes delegate to the base transport unchanged.
func TestHTTPTransportDelegatesNonWorkerScheme(t *testing.T) {
	reg := NewRegistry()
	base := &probeStubRoundTripper{}
	client := &http.Client{Transport: reg.HTTPTransport(base)}
	resp, err := client.Get("http://example.invalid/healthz")
	if err != nil {
		t.Fatalf("delegate error: %v", err)
	}
	defer resp.Body.Close()
	if !base.called {
		t.Fatal("base transport should handle non-worker schemes")
	}
}

// A nil registry yields the base transport untouched.
func TestHTTPTransportNilRegistryReturnsBase(t *testing.T) {
	var reg *Registry
	base := &probeStubRoundTripper{}
	if got := reg.HTTPTransport(base); got != base {
		t.Fatal("nil registry should return the base transport unchanged")
	}
}
