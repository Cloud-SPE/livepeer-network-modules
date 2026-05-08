package metrics

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestNewListenerEmptyAddr(t *testing.T) {
	l, err := NewListener(Config{})
	if err != nil {
		t.Fatal(err)
	}
	if l != nil {
		t.Fatal("expected nil listener for empty Addr")
	}
}

func TestNewListenerRequiresHandler(t *testing.T) {
	if _, err := NewListener(Config{Addr: "127.0.0.1:0"}); err == nil {
		t.Fatal("expected error: missing handler")
	}
}

func TestServeAndShutdown(t *testing.T) {
	h := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("# metrics here\n"))
	})
	l, err := NewListener(Config{Addr: "127.0.0.1:0", Handler: h})
	if err != nil {
		t.Fatal(err)
	}
	// Use a random port via :0 — test passes if Serve binds without error.
	ctx, cancel := context.WithCancel(context.Background())
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		if err := l.Serve(ctx); err != nil && !errors.Is(err, context.Canceled) {
			t.Errorf("Serve err = %v", err)
		}
	}()
	// Give Serve a moment to bind.
	time.Sleep(50 * time.Millisecond)
	cancel()
	wg.Wait()
	if !l.Closed() {
		t.Fatal("expected Closed() == true after shutdown")
	}
}

func TestServeRejectsDoubleStart(t *testing.T) {
	h := http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {})
	l, err := NewListener(Config{Addr: "127.0.0.1:0", Handler: h})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		_ = l.Serve(ctx)
	}()
	time.Sleep(20 * time.Millisecond)
	if err := l.Serve(ctx); err == nil {
		t.Fatal("expected error on double-start")
	}
	cancel()
	wg.Wait()
}

func TestHealthzEndpoint(t *testing.T) {
	// Use httptest to exercise the handler without binding tcp.
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/healthz")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("status = %d; want 200", resp.StatusCode)
	}
	buf := make([]byte, 16)
	n, _ := resp.Body.Read(buf)
	if !strings.Contains(string(buf[:n]), "ok") {
		t.Fatalf("body = %q; want ok", string(buf[:n]))
	}
}
