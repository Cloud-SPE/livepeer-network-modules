package web

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Cloud-SPE/livepeer-network-rewrite/secure-orch-console/internal/audit"
	"github.com/Cloud-SPE/livepeer-network-rewrite/secure-orch-console/internal/config"
	"github.com/Cloud-SPE/livepeer-network-rewrite/secure-orch-console/internal/signing"
)

func newHarness(t *testing.T, listen string) (*Server, func()) {
	t.Helper()
	root := t.TempDir()
	log, err := audit.Open(filepath.Join(root, "log", "audit.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	signer, err := signing.GenerateRandom()
	if err != nil {
		log.Close()
		t.Fatal(err)
	}
	cfg := config.Config{
		LastSignedPath: filepath.Join(root, "lib", "last-signed.json"),
		AuditLogPath:   filepath.Join(root, "log", "audit.jsonl"),
		Listen:         listen,
	}
	srv, err := New(cfg, signer, log, nil)
	if err != nil {
		log.Close()
		signer.Close()
		t.Fatal(err)
	}
	cleanup := func() {
		log.Close()
		signer.Close()
	}
	return srv, cleanup
}

func TestServer_RejectsRoutableBind(t *testing.T) {
	cases := []string{"0.0.0.0:0", ":0"}
	for _, addr := range cases {
		t.Run(addr, func(t *testing.T) {
			srv, cleanup := newHarnessIfPossible(t, addr)
			if srv != nil {
				cleanup()
				t.Fatalf("constructor accepted routable bind %q (hard rule violation)", addr)
			}
		})
	}
}

func newHarnessIfPossible(t *testing.T, listen string) (*Server, func()) {
	t.Helper()
	root := t.TempDir()
	log, err := audit.Open(filepath.Join(root, "log", "audit.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	signer, err := signing.GenerateRandom()
	if err != nil {
		log.Close()
		t.Fatal(err)
	}
	cfg := config.Config{
		LastSignedPath: filepath.Join(root, "lib", "last-signed.json"),
		AuditLogPath:   filepath.Join(root, "log", "audit.jsonl"),
		Listen:         listen,
	}
	srv, err := New(cfg, signer, log, nil)
	if err != nil {
		log.Close()
		signer.Close()
		return nil, nil
	}
	cleanup := func() {
		log.Close()
		signer.Close()
	}
	return srv, cleanup
}

func TestServer_BindsLoopback(t *testing.T) {
	srv, cleanup := newHarness(t, "127.0.0.1:0")
	defer cleanup()
	addr, err := srv.Listen()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(addr.String(), "127.0.0.1:") {
		t.Fatalf("not loopback: %s", addr)
	}
	tcp, ok := addr.(*net.TCPAddr)
	if !ok || !tcp.IP.IsLoopback() {
		t.Fatalf("not loopback: %v", addr)
	}
}

func TestServer_HealthAndIndex(t *testing.T) {
	srv, cleanup := newHarness(t, "127.0.0.1:0")
	defer cleanup()
	if _, err := srv.Listen(); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go srv.Serve(ctx)

	url := "http://" + srv.Addr()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		resp, err := http.Get(url + "/healthz")
		if err == nil {
			resp.Body.Close()
			break
		}
		time.Sleep(20 * time.Millisecond)
	}

	resp, err := http.Get(url + "/healthz")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if string(body) != "ok\n" {
		t.Fatalf("got %q", body)
	}

	resp, err = http.Get(url + "/")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var got map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	if got["signer_address"] == nil {
		t.Fatalf("missing signer_address: %v", got)
	}
}

func TestServer_StubReturns501(t *testing.T) {
	srv, cleanup := newHarness(t, "127.0.0.1:0")
	defer cleanup()
	if _, err := srv.Listen(); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go srv.Serve(ctx)
	time.Sleep(50 * time.Millisecond)
	for _, path := range []string{"/sign", "/candidate"} {
		resp, err := http.Post("http://"+srv.Addr()+path, "application/json", strings.NewReader("{}"))
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusNotImplemented {
			t.Fatalf("path %s got status %d", path, resp.StatusCode)
		}
	}
}

func TestServer_UnknownPath404(t *testing.T) {
	srv, cleanup := newHarness(t, "127.0.0.1:0")
	defer cleanup()
	if _, err := srv.Listen(); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go srv.Serve(ctx)
	time.Sleep(50 * time.Millisecond)
	resp, err := http.Get("http://" + srv.Addr() + "/totally/unmapped")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("got %d", resp.StatusCode)
	}
}
