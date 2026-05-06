package web

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Cloud-SPE/livepeer-network-rewrite/secure-orch-console/internal/audit"
	"github.com/Cloud-SPE/livepeer-network-rewrite/secure-orch-console/internal/config"
	"github.com/Cloud-SPE/livepeer-network-rewrite/secure-orch-console/internal/inbox"
	"github.com/Cloud-SPE/livepeer-network-rewrite/secure-orch-console/internal/outbox"
	"github.com/Cloud-SPE/livepeer-network-rewrite/secure-orch-console/internal/signing"
)

func newHarness(t *testing.T, listen string) (*Server, func()) {
	t.Helper()
	root := t.TempDir()
	inboxDir := filepath.Join(root, "inbox")
	outboxDir := filepath.Join(root, "outbox")
	mkdir(t, inboxDir)
	mkdir(t, outboxDir)
	in, err := inbox.New(inboxDir)
	if err != nil {
		t.Fatal(err)
	}
	out, err := outbox.New(outboxDir, filepath.Join(root, "lib", "last-signed.json"))
	if err != nil {
		t.Fatal(err)
	}
	log, err := audit.Open(filepath.Join(root, "log", "audit.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	signer, err := signing.GenerateRandom()
	if err != nil {
		t.Fatal(err)
	}
	cfg := config.Config{
		Listen: listen,
	}
	srv, err := New(cfg, signer, in, out, log, nil)
	if err != nil {
		log.Close()
		t.Fatal(err)
	}
	cleanup := func() {
		log.Close()
		signer.Close()
	}
	return srv, cleanup
}

func mkdir(t *testing.T, d string) {
	t.Helper()
	if err := os.MkdirAll(d, 0o700); err != nil {
		t.Fatal(err)
	}
}

func TestServer_RejectsRoutableBind(t *testing.T) {
	cases := []string{"0.0.0.0:0", ":0"}
	for _, addr := range cases {
		t.Run(addr, func(t *testing.T) {
			_, cleanup := newHarnessIfPossible(t, addr)
			if cleanup != nil {
				cleanup()
				t.Fatalf("constructor accepted routable bind %q (hard rule violation)", addr)
			}
		})
	}
}

func newHarnessIfPossible(t *testing.T, listen string) (*Server, func()) {
	t.Helper()
	defer func() {
		_ = recover()
	}()
	root := t.TempDir()
	mkdir(t, filepath.Join(root, "inbox"))
	mkdir(t, filepath.Join(root, "outbox"))
	in, err := inbox.New(filepath.Join(root, "inbox"))
	if err != nil {
		t.Fatal(err)
	}
	out, err := outbox.New(filepath.Join(root, "outbox"), filepath.Join(root, "lib", "last-signed.json"))
	if err != nil {
		t.Fatal(err)
	}
	log, err := audit.Open(filepath.Join(root, "log", "audit.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	signer, err := signing.GenerateRandom()
	if err != nil {
		t.Fatal(err)
	}
	srv, err := New(config.Config{Listen: listen}, signer, in, out, log, nil)
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

func TestServer_SignReturns501(t *testing.T) {
	srv, cleanup := newHarness(t, "127.0.0.1:0")
	defer cleanup()
	if _, err := srv.Listen(); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go srv.Serve(ctx)
	time.Sleep(50 * time.Millisecond)
	resp, err := http.Post("http://"+srv.Addr()+"/sign", "application/json", strings.NewReader("{}"))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNotImplemented {
		t.Fatalf("got status %d", resp.StatusCode)
	}
}
