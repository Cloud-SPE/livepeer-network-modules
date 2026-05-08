package metrics

import (
	"context"
	"io"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/Cloud-SPE/livepeer-network-rewrite/service-registry-daemon/internal/providers/logger"
	pmetrics "github.com/Cloud-SPE/livepeer-network-rewrite/service-registry-daemon/internal/providers/metrics"
)

// reservePort grabs a free port by binding once and immediately
// closing the listener. There's a tiny race window between close and
// the next Serve, but `net.Listen("tcp", ":0")` is the standard
// idiom for tests and the next Listen reusing the port is virtually
// always fine.
func reservePort(t *testing.T) string {
	t.Helper()
	ln, err := newTCPListener("127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	_ = ln.Close()
	return addr
}

func TestNewListener_EmptyAddrReturnsNil(t *testing.T) {
	l, err := NewListener(Config{})
	if err != nil {
		t.Fatal(err)
	}
	if l != nil {
		t.Fatal("expected nil listener for empty addr")
	}
}

func TestNewListener_RequiresRecorder(t *testing.T) {
	if _, err := NewListener(Config{Addr: "127.0.0.1:0"}); err == nil {
		t.Fatal("expected error: missing Recorder")
	}
}

func TestServe_MetricsAndHealthz(t *testing.T) {
	rec := pmetrics.NewPrometheus(pmetrics.PrometheusConfig{})
	rec.IncResolution("well_known", "fresh")

	addr := reservePort(t)
	l, err := NewListener(Config{Addr: addr, Recorder: rec, Logger: logger.Discard()})
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- l.Serve(ctx) }()

	deadline := time.Now().Add(3 * time.Second)
	for {
		if _, err := http.Get("http://" + addr + "/healthz"); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("listener never came up")
		}
		time.Sleep(20 * time.Millisecond)
	}

	resp, err := http.Get("http://" + addr + "/healthz")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 200 || !strings.Contains(string(body), "ok") {
		t.Fatalf("/healthz: %d %q", resp.StatusCode, body)
	}

	resp, err = http.Get("http://" + addr + "/metrics")
	if err != nil {
		t.Fatal(err)
	}
	body, _ = io.ReadAll(resp.Body)
	resp.Body.Close()
	if !strings.Contains(string(body), "livepeer_registry_resolutions_total") {
		t.Fatalf("/metrics body missing namespace: %s", body)
	}

	resp, err = http.Get("http://" + addr + "/")
	if err != nil {
		t.Fatal(err)
	}
	body, _ = io.ReadAll(resp.Body)
	resp.Body.Close()
	if !strings.Contains(string(body), "livepeer-service-registry") {
		t.Fatalf("root index unexpected: %s", body)
	}

	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Serve: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Serve did not return")
	}
}

func TestServe_BindFailureSurfaces(t *testing.T) {
	// Bind once, then try to bind a second listener on the same addr.
	rec := pmetrics.NewPrometheus(pmetrics.PrometheusConfig{})
	addr := reservePort(t)
	first, _ := NewListener(Config{Addr: addr, Recorder: rec, Logger: logger.Discard()})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = first.Serve(ctx) }()
	time.Sleep(100 * time.Millisecond)

	second, _ := NewListener(Config{Addr: addr, Recorder: rec, Logger: logger.Discard()})
	if err := second.Serve(context.Background()); err == nil {
		t.Fatal("expected bind error on duplicate port")
	}
}

func TestStop_NilSafe(t *testing.T) {
	var l *Listener
	l.Stop() // must not panic
}

func TestServe_NilWaitsForCtx(t *testing.T) {
	var l *Listener
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { _ = l.Serve(ctx); close(done) }()
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("nil listener Serve did not return on ctx cancel")
	}
}

// newTCPListener returns a real net.Listener. Used only by
// reservePort to find an open TCP port.
func newTCPListener(addr string) (net.Listener, error) {
	return net.Listen("tcp", addr)
}
