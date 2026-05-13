package web

import (
	"archive/tar"
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"mime/multipart"
	"net"
	"net/http"
	"net/http/cookiejar"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	protocolv1 "github.com/Cloud-SPE/livepeer-network-rewrite/proto-contracts/livepeer/protocol/v1"
	"github.com/Cloud-SPE/livepeer-network-rewrite/secure-orch-console/internal/audit"
	"github.com/Cloud-SPE/livepeer-network-rewrite/secure-orch-console/internal/config"
	"github.com/Cloud-SPE/livepeer-network-rewrite/secure-orch-console/internal/signing"
	"google.golang.org/grpc"
)

func newHarness(t *testing.T, listen string) (*Server, string, func()) {
	return newHarnessWithTokens(t, listen, nil)
}

func newHarnessWithTokens(t *testing.T, listen string, adminTokens []string) (*Server, string, func()) {
	t.Helper()
	root := t.TempDir()
	lastSigned := filepath.Join(root, "lib", "last-signed.json")
	auditPath := filepath.Join(root, "log", "audit.jsonl")
	log, err := audit.Open(auditPath, audit.DefaultRotateSize)
	if err != nil {
		t.Fatal(err)
	}
	signer, err := signing.GenerateRandom()
	if err != nil {
		log.Close()
		t.Fatal(err)
	}
	cfg := config.Config{
		LastSignedPath:  lastSigned,
		AuditLogPath:    auditPath,
		AuditRotateSize: audit.DefaultRotateSize,
		Listen:          listen,
		AdminTokens:     adminTokens,
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
	return srv, root, cleanup
}

func TestServer_RejectsAmbiguousBind(t *testing.T) {
	cases := []string{":0"}
	for _, addr := range cases {
		t.Run(addr, func(t *testing.T) {
			srv, cleanup := newHarnessIfPossible(t, addr)
			if srv != nil {
				cleanup()
				t.Fatalf("constructor accepted ambiguous bind %q", addr)
			}
		})
	}
}

func newHarnessIfPossible(t *testing.T, listen string) (*Server, func()) {
	t.Helper()
	root := t.TempDir()
	log, err := audit.Open(filepath.Join(root, "log", "audit.jsonl"), audit.DefaultRotateSize)
	if err != nil {
		t.Fatal(err)
	}
	signer, err := signing.GenerateRandom()
	if err != nil {
		log.Close()
		t.Fatal(err)
	}
	cfg := config.Config{
		LastSignedPath:  filepath.Join(root, "lib", "last-signed.json"),
		AuditLogPath:    filepath.Join(root, "log", "audit.jsonl"),
		AuditRotateSize: audit.DefaultRotateSize,
		Listen:          listen,
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
	srv, _, cleanup := newHarness(t, "127.0.0.1:0")
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

func TestServer_BindsNonLoopbackWhenConfigured(t *testing.T) {
	srv, _, cleanup := newHarness(t, "0.0.0.0:0")
	defer cleanup()
	addr, err := srv.Listen()
	if err != nil {
		t.Fatal(err)
	}
	tcp, ok := addr.(*net.TCPAddr)
	if !ok {
		t.Fatalf("unexpected addr type %T", addr)
	}
	if !tcp.IP.IsUnspecified() {
		t.Fatalf("expected unspecified bind, got %v", tcp.IP)
	}
}

func TestServer_HealthAndIndex(t *testing.T) {
	srv, _, cleanup := newHarness(t, "127.0.0.1:0")
	defer cleanup()
	if _, err := srv.Listen(); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go srv.Serve(ctx)

	url := "http://" + srv.Addr()
	if err := waitFor(url + "/healthz"); err != nil {
		t.Fatal(err)
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
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d", resp.StatusCode)
	}
	if resp.Request.URL.Path != "/protocol-status" {
		t.Fatalf("expected protocol status page, got %s", resp.Request.URL.Path)
	}
	page, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(page), "Protocol status") {
		t.Fatalf("page missing title: %q", page)
	}
	if !strings.Contains(string(page), strings.ToLower(srv.signer.Address().String())) {
		t.Fatalf("page missing signer addr")
	}
}

func TestServer_UnknownPath404(t *testing.T) {
	srv, _, cleanup := newHarness(t, "127.0.0.1:0")
	defer cleanup()
	if _, err := srv.Listen(); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go srv.Serve(ctx)
	if err := waitFor("http://" + srv.Addr() + "/healthz"); err != nil {
		t.Fatal(err)
	}
	resp, err := http.Get("http://" + srv.Addr() + "/totally/unmapped")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("got %d", resp.StatusCode)
	}
}

func TestServer_SignFlow(t *testing.T) {
	srv, _, cleanup := newHarness(t, "127.0.0.1:0")
	defer cleanup()
	if _, err := srv.Listen(); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go srv.Serve(ctx)
	if err := waitFor("http://" + srv.Addr() + "/healthz"); err != nil {
		t.Fatal(err)
	}

	url := "http://" + srv.Addr()
	addr := strings.ToLower(srv.signer.Address().String())

	manifest := `{"manifest":{"spec_version":"0.2.0","publication_seq":1,"issued_at":"2026-05-06T00:00:00Z","expires_at":"2026-06-06T00:00:00Z","orch":{"eth_address":"` + addr + `"},"capabilities":[{"capability_id":"openai:chat","offering_id":"small","price_per_unit_wei":"1000"}]}}`

	uploadCandidate(t, url, "manifest.json", []byte(manifest))

	resp, err := http.Get(url + "/manifests")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	page, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(page), "Tap to sign") {
		t.Fatalf("page missing tap-to-sign: %s", page)
	}

	last4 := lastFourHex(addr)
	form := strings.NewReader("confirm_last4=" + last4)
	resp, err = http.Post(url+"/sign", "application/x-www-form-urlencoded", form)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("sign status %d: %s", resp.StatusCode, body)
	}
	if got := resp.Header.Get("Content-Disposition"); !strings.Contains(got, "signed.json") {
		t.Fatalf("expected attachment, got %q", got)
	}
	body, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(body), `"signature"`) {
		t.Fatalf("response missing signature: %s", body)
	}

	persisted, err := os.ReadFile(srv.cfg.LastSignedPath)
	if err != nil {
		t.Fatalf("last-signed not written: %v", err)
	}
	if !bytes.Contains(persisted, []byte(`"signature"`)) {
		t.Fatal("last-signed file missing signature")
	}
	st, err := os.Stat(srv.cfg.LastSignedPath)
	if err != nil {
		t.Fatal(err)
	}
	if st.Mode().Perm() != 0o600 {
		t.Fatalf("last-signed mode %o, want 0600", st.Mode().Perm())
	}
}

func TestServer_SignRejectsBadConfirm(t *testing.T) {
	srv, _, cleanup := newHarness(t, "127.0.0.1:0")
	defer cleanup()
	if _, err := srv.Listen(); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go srv.Serve(ctx)
	if err := waitFor("http://" + srv.Addr() + "/healthz"); err != nil {
		t.Fatal(err)
	}
	url := "http://" + srv.Addr()
	addr := strings.ToLower(srv.signer.Address().String())
	manifest := `{"manifest":{"spec_version":"0.2.0","publication_seq":1,"orch":{"eth_address":"` + addr + `"},"capabilities":[]}}`
	uploadCandidate(t, url, "manifest.json", []byte(manifest))
	resp, err := http.Post(url+"/sign", "application/x-www-form-urlencoded", strings.NewReader("confirm_last4=ZZZZ"))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", resp.StatusCode)
	}
}

func TestServer_AcceptsTarball(t *testing.T) {
	srv, _, cleanup := newHarness(t, "127.0.0.1:0")
	defer cleanup()
	if _, err := srv.Listen(); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go srv.Serve(ctx)
	if err := waitFor("http://" + srv.Addr() + "/healthz"); err != nil {
		t.Fatal(err)
	}
	url := "http://" + srv.Addr()
	addr := strings.ToLower(srv.signer.Address().String())
	manifest := `{"manifest":{"spec_version":"0.2.0","publication_seq":1,"orch":{"eth_address":"` + addr + `"},"capabilities":[]}}`
	tarball := buildTar(t, []tarFile{
		{"manifest.json", []byte(manifest)},
		{"metadata.json", []byte(`{"source":"coordinator"}`)},
	})
	uploadCandidate(t, url, "candidate.tar", tarball)
	resp, err := http.Get(url + "/manifests")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("status %d: %s", resp.StatusCode, body)
	}
	page, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(page), "Candidate diff") {
		t.Fatalf("missing diff section: %s", page)
	}
}

func TestServer_DiscardCandidate(t *testing.T) {
	srv, _, cleanup := newHarness(t, "127.0.0.1:0")
	defer cleanup()
	if _, err := srv.Listen(); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go srv.Serve(ctx)
	if err := waitFor("http://" + srv.Addr() + "/healthz"); err != nil {
		t.Fatal(err)
	}
	url := "http://" + srv.Addr()
	addr := strings.ToLower(srv.signer.Address().String())
	manifest := `{"manifest":{"spec_version":"0.2.0","publication_seq":1,"orch":{"eth_address":"` + addr + `"},"capabilities":[]}}`
	uploadCandidate(t, url, "manifest.json", []byte(manifest))

	client := &http.Client{CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := client.Post(url+"/discard", "application/x-www-form-urlencoded", nil)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("expected 303, got %d", resp.StatusCode)
	}

	resp, err = http.Get(url + "/manifests")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	page, _ := io.ReadAll(resp.Body)
	if strings.Contains(string(page), "Candidate diff") {
		t.Fatalf("candidate not discarded: %s", page)
	}
}

func TestServer_AuthLoginAndActorAudit(t *testing.T) {
	srv, root, cleanup := newHarnessWithTokens(t, "127.0.0.1:0", []string{"admin-token"})
	defer cleanup()
	if _, err := srv.Listen(); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go srv.Serve(ctx)
	if err := waitFor("http://" + srv.Addr() + "/healthz"); err != nil {
		t.Fatal(err)
	}

	baseURL := "http://" + srv.Addr()
	resp, err := http.Get(baseURL + "/")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.Request.URL.Path != "/login" {
		t.Fatalf("expected redirect to /login, got %s", resp.Request.URL.Path)
	}

	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	client := &http.Client{
		Jar:           jar,
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse },
	}
	loginResp, err := client.Post(
		baseURL+"/login",
		"application/x-www-form-urlencoded",
		strings.NewReader("admin_token=admin-token&actor=operator"),
	)
	if err != nil {
		t.Fatal(err)
	}
	loginResp.Body.Close()
	if loginResp.StatusCode != http.StatusSeeOther {
		t.Fatalf("expected 303, got %d", loginResp.StatusCode)
	}
	if got := loginResp.Header.Get("Set-Cookie"); !strings.Contains(got, "Max-Age=43200") {
		t.Fatalf("expected 12h session cookie, got %q", got)
	}

	addr := strings.ToLower(srv.signer.Address().String())
	manifest := `{"manifest":{"spec_version":"0.2.0","publication_seq":1,"orch":{"eth_address":"` + addr + `"},"capabilities":[]}}`
	uploadCandidateWithClient(t, client, baseURL, "manifest.json", []byte(manifest))

	signResp, err := client.Post(baseURL+"/sign", "application/x-www-form-urlencoded", strings.NewReader("confirm_last4="+lastFourHex(addr)))
	if err != nil {
		t.Fatal(err)
	}
	signResp.Body.Close()
	if signResp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", signResp.StatusCode)
	}

	events := readAuditEvents(t, filepath.Join(root, "log", "audit.jsonl"))
	found := false
	for _, event := range events {
		if event.Kind == audit.KindSign {
			if event.Actor != "operator" {
				t.Fatalf("sign actor = %q, want operator", event.Actor)
			}
			found = true
		}
	}
	if !found {
		t.Fatal("expected sign audit event")
	}
}

func TestServer_RendersProtocolStatus(t *testing.T) {
	socket := filepath.Join(t.TempDir(), "protocol.sock")
	protoServer := grpc.NewServer()
	protocolv1.RegisterProtocolDaemonServer(protoServer, &fakeProtocolDaemonServer{})
	lis, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	defer lis.Close()
	defer protoServer.Stop()
	go protoServer.Serve(lis)

	root := t.TempDir()
	lastSigned := filepath.Join(root, "lib", "last-signed.json")
	auditPath := filepath.Join(root, "log", "audit.jsonl")
	log, err := audit.Open(auditPath, audit.DefaultRotateSize)
	if err != nil {
		t.Fatal(err)
	}
	defer log.Close()
	signer, err := signing.GenerateRandom()
	if err != nil {
		t.Fatal(err)
	}
	defer signer.Close()
	srv, err := New(config.Config{
		LastSignedPath:  lastSigned,
		AuditLogPath:    auditPath,
		AuditRotateSize: audit.DefaultRotateSize,
		Listen:          "127.0.0.1:0",
		ProtocolSocket:  socket,
	}, signer, log, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := srv.Listen(); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go srv.Serve(ctx)
	if err := waitFor("http://" + srv.Addr() + "/healthz"); err != nil {
		t.Fatal(err)
	}
	resp, err := http.Get("http://" + srv.Addr() + "/protocol-status")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	page := string(body)
	if !strings.Contains(page, "Protocol status") {
		t.Fatalf("missing protocol status: %s", page)
	}
	if !strings.Contains(page, "AI Service Registry") {
		t.Fatalf("missing AI Service Registry: %s", page)
	}
	if !strings.Contains(page, "0x00000000000000000000000000000000000000aa") {
		t.Fatalf("missing wallet address: %s", page)
	}
}

func TestServer_ProtocolActionSubmittedAndAudited(t *testing.T) {
	socket := filepath.Join(t.TempDir(), "protocol.sock")
	protoServer := grpc.NewServer()
	protocolv1.RegisterProtocolDaemonServer(protoServer, &fakeProtocolDaemonServer{
		forceInitializeRoundFn: func(context.Context, *protocolv1.Empty) (*protocolv1.ForceOutcome, error) {
			return &protocolv1.ForceOutcome{
				Outcome: &protocolv1.ForceOutcome_Submitted{
					Submitted: &protocolv1.TxIntentRef{Id: []byte{0xfe, 0xed}},
				},
			}, nil
		},
	})
	lis, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	defer lis.Close()
	defer protoServer.Stop()
	go protoServer.Serve(lis)

	root := t.TempDir()
	lastSigned := filepath.Join(root, "lib", "last-signed.json")
	auditPath := filepath.Join(root, "log", "audit.jsonl")
	log, err := audit.Open(auditPath, audit.DefaultRotateSize)
	if err != nil {
		t.Fatal(err)
	}
	defer log.Close()
	signer, err := signing.GenerateRandom()
	if err != nil {
		t.Fatal(err)
	}
	defer signer.Close()
	srv, err := New(config.Config{
		LastSignedPath:  lastSigned,
		AuditLogPath:    auditPath,
		AuditRotateSize: audit.DefaultRotateSize,
		Listen:          "127.0.0.1:0",
		ProtocolSocket:  socket,
	}, signer, log, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := srv.Listen(); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go srv.Serve(ctx)
	if err := waitFor("http://" + srv.Addr() + "/healthz"); err != nil {
		t.Fatal(err)
	}

	client := &http.Client{CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := client.Post(
		"http://"+srv.Addr()+"/protocol/force-init",
		"application/x-www-form-urlencoded",
		strings.NewReader("typed_confirmation="+strings.ToLower(signer.Address().String())),
	)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("expected 303, got %d", resp.StatusCode)
	}
	if got := resp.Header.Get("Location"); !strings.Contains(got, "submitted+intent+feed") {
		t.Fatalf("unexpected redirect %q", got)
	}

	events := readAuditEvents(t, auditPath)
	found := false
	for _, event := range events {
		if event.Kind != audit.KindProtocolAction {
			continue
		}
		if event.Fields["action"] != "chain.round.init" || event.Fields["result"] != "success" {
			continue
		}
		data, _ := event.Fields["data"].(map[string]any)
		if data["intent_id"] != "feed" {
			t.Fatalf("intent id = %#v, want feed", data["intent_id"])
		}
		found = true
	}
	if !found {
		t.Fatal("expected protocol action audit event")
	}
}

func TestServer_ProtocolActionRejectsBadTypedConfirmation(t *testing.T) {
	socket := filepath.Join(t.TempDir(), "protocol.sock")
	protoServer := grpc.NewServer()
	var called atomic.Int32
	protocolv1.RegisterProtocolDaemonServer(protoServer, &fakeProtocolDaemonServer{
		forceRewardCallFn: func(context.Context, *protocolv1.Empty) (*protocolv1.ForceOutcome, error) {
			called.Add(1)
			return &protocolv1.ForceOutcome{}, nil
		},
	})
	lis, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	defer lis.Close()
	defer protoServer.Stop()
	go protoServer.Serve(lis)

	root := t.TempDir()
	lastSigned := filepath.Join(root, "lib", "last-signed.json")
	auditPath := filepath.Join(root, "log", "audit.jsonl")
	log, err := audit.Open(auditPath, audit.DefaultRotateSize)
	if err != nil {
		t.Fatal(err)
	}
	defer log.Close()
	signer, err := signing.GenerateRandom()
	if err != nil {
		t.Fatal(err)
	}
	defer signer.Close()
	srv, err := New(config.Config{
		LastSignedPath:  lastSigned,
		AuditLogPath:    auditPath,
		AuditRotateSize: audit.DefaultRotateSize,
		Listen:          "127.0.0.1:0",
		ProtocolSocket:  socket,
	}, signer, log, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := srv.Listen(); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go srv.Serve(ctx)
	if err := waitFor("http://" + srv.Addr() + "/healthz"); err != nil {
		t.Fatal(err)
	}

	client := &http.Client{CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := client.Post(
		"http://"+srv.Addr()+"/protocol/force-reward",
		"application/x-www-form-urlencoded",
		strings.NewReader("typed_confirmation=0x0000000000000000000000000000000000000000"),
	)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("expected 303, got %d", resp.StatusCode)
	}
	if called.Load() != 0 {
		t.Fatalf("expected protocol RPC not to be called, got %d", called.Load())
	}
	events := readAuditEvents(t, auditPath)
	found := false
	for _, event := range events {
		if event.Kind != audit.KindProtocolAction {
			continue
		}
		if event.Fields["action"] != "chain.reward.claim" || event.Fields["result"] != "rejected" {
			continue
		}
		found = true
	}
	if !found {
		t.Fatal("expected rejected protocol action audit event")
	}
}

func TestServer_RendersTxIntentLookupAndAuditLog(t *testing.T) {
	socket := filepath.Join(t.TempDir(), "protocol.sock")
	protoServer := grpc.NewServer()
	protocolv1.RegisterProtocolDaemonServer(protoServer, &fakeProtocolDaemonServer{})
	lis, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	defer lis.Close()
	defer protoServer.Stop()
	go protoServer.Serve(lis)

	root := t.TempDir()
	lastSigned := filepath.Join(root, "lib", "last-signed.json")
	auditPath := filepath.Join(root, "log", "audit.jsonl")
	log, err := audit.Open(auditPath, audit.DefaultRotateSize)
	if err != nil {
		t.Fatal(err)
	}
	defer log.Close()
	signer, err := signing.GenerateRandom()
	if err != nil {
		t.Fatal(err)
	}
	defer signer.Close()
	srv, err := New(config.Config{
		LastSignedPath:  lastSigned,
		AuditLogPath:    auditPath,
		AuditRotateSize: audit.DefaultRotateSize,
		Listen:          "127.0.0.1:0",
		ProtocolSocket:  socket,
	}, signer, log, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := srv.Listen(); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go srv.Serve(ctx)
	if err := waitFor("http://" + srv.Addr() + "/healthz"); err != nil {
		t.Fatal(err)
	}

	addr := strings.ToLower(srv.signer.Address().String())
	manifest := `{"manifest":{"spec_version":"0.2.0","publication_seq":1,"orch":{"eth_address":"` + addr + `"},"capabilities":[]}}`
	uploadCandidate(t, "http://"+srv.Addr(), "manifest.json", []byte(manifest))

	resp, err := http.Get("http://" + srv.Addr() + "/protocol-actions?tx_intent_id=0xfeed")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	page := string(body)
	if !strings.Contains(page, "Tx intent lookup") {
		t.Fatalf("missing tx intent section: %s", page)
	}
	if !strings.Contains(page, "0xfeed") {
		t.Fatalf("missing intent id: %s", page)
	}
	if !strings.Contains(page, "confirmed") {
		t.Fatalf("missing intent status: %s", page)
	}
	if strings.Contains(page, "Audit log") {
		t.Fatalf("protocol actions page should not inline audit log: %s", page)
	}

	resp, err = http.Get("http://" + srv.Addr() + "/audit")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ = io.ReadAll(resp.Body)
	page = string(body)
	if !strings.Contains(page, "Audit") {
		t.Fatalf("missing audit page: %s", page)
	}
	if !strings.Contains(page, "load_candidate") {
		t.Fatalf("missing audit event in page: %s", page)
	}
}

func TestServer_AuditCursorPagination(t *testing.T) {
	srv, root, cleanup := newHarness(t, "127.0.0.1:0")
	defer cleanup()
	if _, err := srv.Listen(); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 12; i++ {
		if err := srv.audit.Append(audit.Event{
			At:   time.Unix(int64(i), 0).UTC(),
			Kind: audit.KindProtocolAction,
			Note: "event-" + strconv.Itoa(i),
		}); err != nil {
			t.Fatal(err)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go srv.Serve(ctx)
	if err := waitFor("http://" + srv.Addr() + "/healthz"); err != nil {
		t.Fatal(err)
	}

	resp, err := http.Get("http://" + srv.Addr() + "/audit")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	page := string(body)
	if strings.Count(page, "class=\"status-card audit-event\"") != 10 {
		t.Fatalf("expected 10 audit cards on first page, got page %s", page)
	}
	if !strings.Contains(page, "Older") {
		t.Fatalf("missing older link: %s", page)
	}
	if strings.Contains(page, "<dt>note</dt><dd>event-1</dd>") || strings.Contains(page, "<dt>note</dt><dd>event-0</dd>") {
		t.Fatalf("first page should not contain oldest events: %s", page)
	}

	events := readAuditEvents(t, filepath.Join(root, "log", "audit.jsonl"))
	firstPage, err := audit.ReadPage(filepath.Join(root, "log", "audit.jsonl"), auditPageSize, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 12 || firstPage.NextCursor == "" {
		t.Fatalf("expected cursor for second page")
	}

	resp, err = http.Get("http://" + srv.Addr() + "/audit?before=" + firstPage.NextCursor)
	if err != nil {
		t.Fatal(err)
	}
	body, _ = io.ReadAll(resp.Body)
	resp.Body.Close()
	page = string(body)
	if !strings.Contains(page, "event-1") || !strings.Contains(page, "event-0") {
		t.Fatalf("older page missing oldest events: %s", page)
	}
	if strings.Count(page, "class=\"status-card audit-event\"") != 2 {
		t.Fatalf("expected 2 audit cards on second page, got page %s", page)
	}
}

func uploadCandidate(t *testing.T, baseURL, filename string, body []byte) {
	t.Helper()
	client := &http.Client{CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }}
	uploadCandidateWithClient(t, client, baseURL, filename, body)
}

func uploadCandidateWithClient(t *testing.T, client *http.Client, baseURL, filename string, body []byte) {
	t.Helper()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	fw, err := mw.CreateFormFile("candidate", filename)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fw.Write(body); err != nil {
		t.Fatal(err)
	}
	mw.Close()
	req, _ := http.NewRequest(http.MethodPost, baseURL+"/candidate", &buf)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("upload: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusSeeOther {
		respBody, _ := io.ReadAll(resp.Body)
		t.Fatalf("upload status %d: %s", resp.StatusCode, respBody)
	}
}

func readAuditEvents(t *testing.T, path string) []audit.Event {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	var out []audit.Event
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		var event audit.Event
		if err := json.Unmarshal(scanner.Bytes(), &event); err != nil {
			t.Fatal(err)
		}
		out = append(out, event)
	}
	if err := scanner.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}

type tarFile struct {
	name string
	data []byte
}

func buildTar(t *testing.T, files []tarFile) []byte {
	t.Helper()
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	for _, f := range files {
		if err := tw.WriteHeader(&tar.Header{Name: f.name, Mode: 0o600, Size: int64(len(f.data))}); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write(f.data); err != nil {
			t.Fatal(err)
		}
	}
	tw.Close()
	return buf.Bytes()
}

func waitFor(url string) error {
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		resp, err := http.Get(url)
		if err == nil {
			resp.Body.Close()
			return nil
		}
		time.Sleep(20 * time.Millisecond)
	}
	return io.ErrUnexpectedEOF
}

type fakeProtocolDaemonServer struct {
	protocolv1.UnimplementedProtocolDaemonServer
	forceInitializeRoundFn func(context.Context, *protocolv1.Empty) (*protocolv1.ForceOutcome, error)
	forceRewardCallFn      func(context.Context, *protocolv1.Empty) (*protocolv1.ForceOutcome, error)
	setServiceURIFn        func(context.Context, *protocolv1.SetServiceURIRequest) (*protocolv1.TxIntentRef, error)
	setAIServiceURIFn      func(context.Context, *protocolv1.SetAIServiceURIRequest) (*protocolv1.TxIntentRef, error)
	getTxIntentFn          func(context.Context, *protocolv1.TxIntentRef) (*protocolv1.TxIntentSnapshot, error)
}

func (fakeProtocolDaemonServer) Health(context.Context, *protocolv1.Empty) (*protocolv1.HealthStatus, error) {
	return &protocolv1.HealthStatus{Ok: true, Mode: "both", Version: "test", ChainId: 42161}, nil
}

func (fakeProtocolDaemonServer) GetRoundStatus(context.Context, *protocolv1.Empty) (*protocolv1.RoundStatus, error) {
	return &protocolv1.RoundStatus{LastRound: 12, CurrentRoundInitialized: true}, nil
}

func (fakeProtocolDaemonServer) GetRewardStatus(context.Context, *protocolv1.Empty) (*protocolv1.RewardStatus, error) {
	return &protocolv1.RewardStatus{LastRound: 12, Eligible: true, OrchAddress: []byte{0x11}, LastEarnedWei: []byte{0x01}}, nil
}

func (fakeProtocolDaemonServer) GetOnChainServiceURI(context.Context, *protocolv1.Empty) (*protocolv1.OnChainServiceURIStatus, error) {
	return &protocolv1.OnChainServiceURIStatus{Url: "https://coordinator.example.com/.well-known/livepeer-registry.json"}, nil
}

func (fakeProtocolDaemonServer) GetOnChainAIServiceURI(context.Context, *protocolv1.Empty) (*protocolv1.OnChainAIServiceURIStatus, error) {
	return &protocolv1.OnChainAIServiceURIStatus{Url: "https://coordinator.example.com/.well-known/livepeer-ai-registry.json"}, nil
}

func (fakeProtocolDaemonServer) IsRegistered(context.Context, *protocolv1.Empty) (*protocolv1.RegistrationStatus, error) {
	return &protocolv1.RegistrationStatus{Registered: true}, nil
}

func (fakeProtocolDaemonServer) IsAIRegistered(context.Context, *protocolv1.Empty) (*protocolv1.AIRegistrationStatus, error) {
	return &protocolv1.AIRegistrationStatus{Registered: true}, nil
}

func (fakeProtocolDaemonServer) GetWalletBalance(context.Context, *protocolv1.Empty) (*protocolv1.WalletBalanceStatus, error) {
	return &protocolv1.WalletBalanceStatus{WalletAddress: []byte{0xaa}, BalanceWei: []byte{0x10}}, nil
}

func (s *fakeProtocolDaemonServer) ForceInitializeRound(ctx context.Context, req *protocolv1.Empty) (*protocolv1.ForceOutcome, error) {
	if s.forceInitializeRoundFn != nil {
		return s.forceInitializeRoundFn(ctx, req)
	}
	return &protocolv1.ForceOutcome{
		Outcome: &protocolv1.ForceOutcome_Skipped{
			Skipped: &protocolv1.SkipReason{
				Reason: "round already initialized",
				Code:   protocolv1.SkipReason_CODE_ROUND_INITIALIZED,
			},
		},
	}, nil
}

func (s *fakeProtocolDaemonServer) ForceRewardCall(ctx context.Context, req *protocolv1.Empty) (*protocolv1.ForceOutcome, error) {
	if s.forceRewardCallFn != nil {
		return s.forceRewardCallFn(ctx, req)
	}
	return &protocolv1.ForceOutcome{
		Outcome: &protocolv1.ForceOutcome_Skipped{
			Skipped: &protocolv1.SkipReason{
				Reason: "already rewarded this round",
				Code:   protocolv1.SkipReason_CODE_ALREADY_REWARDED,
			},
		},
	}, nil
}

func (s *fakeProtocolDaemonServer) SetServiceURI(ctx context.Context, req *protocolv1.SetServiceURIRequest) (*protocolv1.TxIntentRef, error) {
	if s.setServiceURIFn != nil {
		return s.setServiceURIFn(ctx, req)
	}
	return &protocolv1.TxIntentRef{Id: []byte{0xfa, 0xce}}, nil
}

func (s *fakeProtocolDaemonServer) SetAIServiceURI(ctx context.Context, req *protocolv1.SetAIServiceURIRequest) (*protocolv1.TxIntentRef, error) {
	if s.setAIServiceURIFn != nil {
		return s.setAIServiceURIFn(ctx, req)
	}
	return &protocolv1.TxIntentRef{Id: []byte{0xbe, 0xef}}, nil
}

func (s *fakeProtocolDaemonServer) GetTxIntent(ctx context.Context, req *protocolv1.TxIntentRef) (*protocolv1.TxIntentSnapshot, error) {
	if s.getTxIntentFn != nil {
		return s.getTxIntentFn(ctx, req)
	}
	return &protocolv1.TxIntentSnapshot{
		Id:                    req.GetId(),
		Kind:                  "set_ai_service_uri",
		Status:                "confirmed",
		CreatedAtUnixNano:     uint64(time.Date(2026, 5, 9, 20, 0, 0, 0, time.UTC).UnixNano()),
		LastUpdatedAtUnixNano: uint64(time.Date(2026, 5, 9, 20, 1, 0, 0, time.UTC).UnixNano()),
		ConfirmedAtUnixNano:   uint64(time.Date(2026, 5, 9, 20, 2, 0, 0, time.UTC).UnixNano()),
		AttemptCount:          1,
	}, nil
}
