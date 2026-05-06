package web

import (
	"archive/tar"
	"bytes"
	"context"
	"io"
	"mime/multipart"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Cloud-SPE/livepeer-network-rewrite/secure-orch-console/internal/audit"
	"github.com/Cloud-SPE/livepeer-network-rewrite/secure-orch-console/internal/config"
	"github.com/Cloud-SPE/livepeer-network-rewrite/secure-orch-console/internal/signing"
)

func newHarness(t *testing.T, listen string) (*Server, string, func()) {
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
	page, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(page), "secure-orch console") {
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

	resp, err := http.Get(url + "/")
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
	resp, err := http.Get(url + "/")
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

	resp, err = http.Get(url + "/")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	page, _ := io.ReadAll(resp.Body)
	if strings.Contains(string(page), "Candidate diff") {
		t.Fatalf("candidate not discarded: %s", page)
	}
}

func uploadCandidate(t *testing.T, baseURL, filename string, body []byte) {
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
	client := &http.Client{CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }}
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
