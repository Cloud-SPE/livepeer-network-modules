package multi

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Cloud-SPE/livepeer-network-rewrite/chain-commons/config"
	cerrors "github.com/Cloud-SPE/livepeer-network-rewrite/chain-commons/errors"
)

// fakeRPCServer responds to JSON-RPC calls. handler is invoked with the
// "method" string parsed from the JSON-RPC request; it returns a JSON-RPC
// result blob (the `result` field). To return an error, return an empty
// string and a non-nil error.
type fakeRPCServer struct {
	srv     *httptest.Server
	calls   atomic.Int32
	handler func(method string) (string, error)
}

func newFakeRPCServer(handler func(method string) (string, error)) *fakeRPCServer {
	f := &fakeRPCServer{handler: handler}
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.calls.Add(1)
		b, _ := io.ReadAll(r.Body)
		body := string(b)
		method := extractJSONRPCMethod(body)
		result, err := f.handler(method)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		fmt.Fprintf(w, `{"jsonrpc":"2.0","id":1,"result":%s}`, result)
	}))
	return f
}

func (f *fakeRPCServer) URL() string { return f.srv.URL }
func (f *fakeRPCServer) Close()      { f.srv.Close() }
func (f *fakeRPCServer) Calls() int  { return int(f.calls.Load()) }

// extractJSONRPCMethod is a brittle but sufficient parser for this test
// purpose. JSON-RPC payloads from go-ethereum look like:
//
//	{"jsonrpc":"2.0","method":"eth_chainId","params":[],"id":1}
func extractJSONRPCMethod(body string) string {
	idx := strings.Index(body, `"method":"`)
	if idx < 0 {
		return ""
	}
	rest := body[idx+len(`"method":"`):]
	end := strings.IndexByte(rest, '"')
	if end < 0 {
		return ""
	}
	return rest[:end]
}

func defaultPolicy() config.RPCPolicy {
	p := config.RPCPolicy{}
	applyDefaults(&p)
	// Tighten timing for tests.
	p.MaxRetries = 2
	p.InitialBackoff = 5 * time.Millisecond
	p.MaxBackoff = 50 * time.Millisecond
	p.HealthProbeInterval = 50 * time.Millisecond
	p.CircuitBreakerThreshold = 3
	p.CircuitBreakerCooloff = 100 * time.Millisecond
	p.CallTimeout = 5 * time.Second
	return p
}

func TestOpen_RequiresURLs(t *testing.T) {
	if _, err := Open(Options{}); err == nil {
		t.Errorf("Open with empty URLs should fail")
	}
}

func TestOpen_DialsAllConfiguredURLs(t *testing.T) {
	srv1 := newFakeRPCServer(func(string) (string, error) { return `"0xa4b1"`, nil }) // 42161
	defer srv1.Close()
	srv2 := newFakeRPCServer(func(string) (string, error) { return `"0xa4b1"`, nil })
	defer srv2.Close()

	m, err := Open(Options{
		URLs:   []string{srv1.URL(), srv2.URL()},
		Policy: defaultPolicy(),
	})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer m.Close()

	eps := m.Endpoints()
	if len(eps) != 2 {
		t.Errorf("Endpoints len = %d, want 2", len(eps))
	}
	if eps[0].Role != "primary" || eps[1].Role != "backup" {
		t.Errorf("roles = %v, want primary,backup", []string{eps[0].Role, eps[1].Role})
	}
	if eps[0].CircuitState != "closed" {
		t.Errorf("primary CircuitState = %q, want closed", eps[0].CircuitState)
	}
}

func TestChainID_RoutesToPrimary(t *testing.T) {
	primary := newFakeRPCServer(func(string) (string, error) { return `"0xa4b1"`, nil })
	defer primary.Close()
	backup := newFakeRPCServer(func(string) (string, error) { return `"0xa4b1"`, nil })
	defer backup.Close()

	m, _ := Open(Options{URLs: []string{primary.URL(), backup.URL()}, Policy: defaultPolicy()})
	defer m.Close()

	id, err := m.ChainID(context.Background())
	if err != nil {
		t.Fatalf("ChainID: %v", err)
	}
	if id != 42161 {
		t.Errorf("ChainID = %d, want 42161", id)
	}
	if primary.Calls() == 0 {
		t.Errorf("primary should receive call")
	}
	if backup.Calls() != 0 {
		t.Errorf("backup should not be called when primary is healthy: got %d", backup.Calls())
	}
}

func TestFailover_OnPrimaryDown(t *testing.T) {
	primary := newFakeRPCServer(func(string) (string, error) {
		return "", errors.New("primary down")
	})
	defer primary.Close()
	backup := newFakeRPCServer(func(string) (string, error) { return `"0xa4b1"`, nil })
	defer backup.Close()

	policy := defaultPolicy()
	policy.MaxRetries = 0 // give primary a single shot, then fail over

	m, _ := Open(Options{URLs: []string{primary.URL(), backup.URL()}, Policy: policy})
	defer m.Close()

	id, err := m.ChainID(context.Background())
	if err != nil {
		t.Fatalf("ChainID: %v", err)
	}
	if id != 42161 {
		t.Errorf("ChainID = %d, want 42161 (from backup)", id)
	}
	if backup.Calls() == 0 {
		t.Errorf("backup should have been called after primary failed")
	}
}

func TestCircuitBreaker_OpensAfterThresholdFailures(t *testing.T) {
	primary := newFakeRPCServer(func(string) (string, error) {
		return "", errors.New("always down")
	})
	defer primary.Close()
	backup := newFakeRPCServer(func(string) (string, error) { return `"0xa4b1"`, nil })
	defer backup.Close()

	policy := defaultPolicy()
	policy.MaxRetries = 0 // each call counts as one failure, no retries
	policy.CircuitBreakerThreshold = 3

	m, _ := Open(Options{URLs: []string{primary.URL(), backup.URL()}, Policy: policy})
	defer m.Close()

	for i := 0; i < 4; i++ {
		_, _ = m.ChainID(context.Background())
	}
	eps := m.Endpoints()
	if eps[0].CircuitState != "open" {
		t.Errorf("primary CircuitState = %q, want open after threshold failures (consec=%d)",
			eps[0].CircuitState, eps[0].ConsecutiveFailures)
	}
}

func TestAllCircuitsOpen_ReturnsCircuitOpenError(t *testing.T) {
	primary := newFakeRPCServer(func(string) (string, error) { return "", errors.New("down") })
	defer primary.Close()

	policy := defaultPolicy()
	policy.MaxRetries = 0
	policy.CircuitBreakerThreshold = 1
	policy.HealthProbeInterval = time.Hour     // disable probe for this test
	policy.CircuitBreakerCooloff = time.Hour

	m, _ := Open(Options{URLs: []string{primary.URL()}, Policy: policy})
	defer m.Close()

	// First call: opens the circuit.
	_, _ = m.ChainID(context.Background())
	// Second call: every endpoint is now open.
	_, err := m.ChainID(context.Background())
	if err == nil {
		t.Fatalf("expected ClassCircuitOpen error, got nil")
	}
	classified := cerrors.Classify(err)
	if classified.Class != cerrors.ClassCircuitOpen {
		t.Errorf("err class = %v, want ClassCircuitOpen", classified.Class)
	}
}

func TestRetryWithBackoff_RecoversWithinAttempts(t *testing.T) {
	var calls atomic.Int32
	primary := newFakeRPCServer(func(string) (string, error) {
		n := calls.Add(1)
		if n < 3 {
			return "", errors.New("transient")
		}
		return `"0xa4b1"`, nil
	})
	defer primary.Close()

	policy := defaultPolicy()
	policy.MaxRetries = 5
	policy.InitialBackoff = 1 * time.Millisecond
	policy.CircuitBreakerThreshold = 100 // don't open mid-retry

	m, _ := Open(Options{URLs: []string{primary.URL()}, Policy: policy})
	defer m.Close()

	id, err := m.ChainID(context.Background())
	if err != nil {
		t.Fatalf("ChainID: %v", err)
	}
	if id != 42161 {
		t.Errorf("ChainID = %d, want 42161", id)
	}
	if calls.Load() != 3 {
		t.Errorf("expected 3 calls (2 fail + 1 success), got %d", calls.Load())
	}
}

func TestBackoffDuration_ExponentialAndCapped(t *testing.T) {
	p := config.RPCPolicy{
		InitialBackoff: 100 * time.Millisecond,
		BackoffFactor:  2.0,
		MaxBackoff:     1 * time.Second,
	}
	cases := []struct {
		attempt int
		want    time.Duration
	}{
		{0, 100 * time.Millisecond},
		{1, 200 * time.Millisecond},
		{2, 400 * time.Millisecond},
		{3, 800 * time.Millisecond},
		{4, 1 * time.Second}, // capped
		{5, 1 * time.Second}, // still capped
	}
	for _, tt := range cases {
		got := backoffDuration(p, tt.attempt)
		if got != tt.want {
			t.Errorf("backoffDuration(%d) = %v, want %v", tt.attempt, got, tt.want)
		}
	}
}

func TestEndpoints_RolesAssigned(t *testing.T) {
	srv := newFakeRPCServer(func(string) (string, error) { return `"0xa4b1"`, nil })
	defer srv.Close()

	m, _ := Open(Options{URLs: []string{srv.URL(), srv.URL(), srv.URL()}, Policy: defaultPolicy()})
	defer m.Close()

	eps := m.Endpoints()
	if eps[0].Role != "primary" {
		t.Errorf("0: role = %q, want primary", eps[0].Role)
	}
	for i := 1; i < len(eps); i++ {
		if eps[i].Role != "backup" {
			t.Errorf("%d: role = %q, want backup", i, eps[i].Role)
		}
	}
}
