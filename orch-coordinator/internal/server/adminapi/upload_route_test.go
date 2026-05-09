package adminapi

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"encoding/hex"
	"encoding/json"
	"io"
	"log/slog"
	"mime/multipart"
	"net/http"
	"net/http/cookiejar"
	"path/filepath"
	"strings"
	"testing"
	"time"

	ethcrypto "github.com/ethereum/go-ethereum/crypto"

	"github.com/Cloud-SPE/livepeer-network-rewrite/livepeer-network-protocol/verify"
	"github.com/Cloud-SPE/livepeer-network-rewrite/orch-coordinator/internal/repo/audit"
	"github.com/Cloud-SPE/livepeer-network-rewrite/orch-coordinator/internal/repo/published"
	"github.com/Cloud-SPE/livepeer-network-rewrite/orch-coordinator/internal/service/candidate"
	"github.com/Cloud-SPE/livepeer-network-rewrite/orch-coordinator/internal/service/receive"
	"github.com/Cloud-SPE/livepeer-network-rewrite/orch-coordinator/internal/types"
)

func newRecv(t *testing.T) (*receive.Service, *audit.Log, *ecdsa.PrivateKey, string) {
	t.Helper()
	priv, err := ethcrypto.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	addr := strings.ToLower(ethcrypto.PubkeyToAddress(priv.PublicKey).Hex())
	dir := t.TempDir()
	store, err := published.New(filepath.Join(dir, "p"))
	if err != nil {
		t.Fatal(err)
	}
	log, err := audit.Open(filepath.Join(dir, "a.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { log.Close() })
	return receive.New(store, nil, log, addr, candidate.SpecVersion, nil), log, priv, addr
}

func signedBody(t *testing.T, priv *ecdsa.PrivateKey, addr string, seq uint64) []byte {
	t.Helper()
	now := time.Now().UTC()
	p := types.ManifestPayload{
		SpecVersion:    candidate.SpecVersion,
		PublicationSeq: seq,
		IssuedAt:       now,
		ExpiresAt:      now.Add(time.Hour),
		Orch:           types.Orch{EthAddress: addr},
		Capabilities: []types.CapabilityTuple{{
			CapabilityID: "c", OfferingID: "o", InteractionMode: "m@v1",
			WorkUnit: types.WorkUnit{Name: "x"}, PricePerUnitWei: "1",
			WorkerURL: "https://w.example/",
		}},
	}
	root := map[string]any{
		"spec_version":    p.SpecVersion,
		"publication_seq": p.PublicationSeq,
		"issued_at":       p.IssuedAt.UTC().Format(time.RFC3339Nano),
		"expires_at":      p.ExpiresAt.UTC().Format(time.RFC3339Nano),
		"orch": map[string]any{
			"eth_address": p.Orch.EthAddress,
		},
		"capabilities": []any{
			map[string]any{
				"capability_id":      "c",
				"offering_id":        "o",
				"interaction_mode":   "m@v1",
				"work_unit":          map[string]any{"name": "x"},
				"price_per_unit_wei": "1",
				"worker_url":         "https://w.example/",
			},
		},
	}
	canon, err := candidate.CanonicalBytes(root)
	if err != nil {
		t.Fatal(err)
	}
	digest := verify.PersonalSignDigest(canon)
	sig, err := ethcrypto.Sign(digest, priv)
	if err != nil {
		t.Fatal(err)
	}
	if sig[64] < 27 {
		sig[64] += 27
	}
	body, err := json.Marshal(types.SignedManifest{
		Manifest: p,
		Signature: types.Signature{
			Algorithm: "secp256k1",
			Value:     "0x" + hex.EncodeToString(sig),
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	return body
}

func TestUpload_Multipart_HappyPath(t *testing.T) {
	rec, _, priv, addr := newRecv(t)
	srv := New("127.0.0.1:0", slog.Default(), nil)
	srv.UploadRoutes(rec)
	if _, err := srv.Listen(); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go srv.Serve(ctx)

	body := signedBody(t, priv, addr, 1)

	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	fw, err := w.CreateFormFile("manifest", "signed.json")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fw.Write(body); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	req, err := http.NewRequest(http.MethodPost, "http://"+srv.Addr()+"/admin/signed-manifest", &buf)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", w.FormDataContentType())

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d body %s", resp.StatusCode, respBody)
	}
}

func TestUpload_RejectedBodyReturnsStableOutcome(t *testing.T) {
	rec, _, _, _ := newRecv(t)
	srv := New("127.0.0.1:0", slog.Default(), nil)
	srv.UploadRoutes(rec)
	if _, err := srv.Listen(); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go srv.Serve(ctx)

	resp, err := http.Post("http://"+srv.Addr()+"/admin/signed-manifest",
		"application/json", strings.NewReader(`{"not":"valid"}`))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusOK {
		t.Fatal("expected non-200 for malformed body")
	}
	var got map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	if got["outcome"] == nil {
		t.Fatalf("missing outcome: %+v", got)
	}
}

func TestUpload_AuthActorRecordedInAudit(t *testing.T) {
	rec, auditLog, priv, addr := newRecv(t)
	srv := New("127.0.0.1:0", slog.Default(), []string{"admin-token"})
	if err := srv.WebRoutes(WebDeps{
		Audit:          auditLog,
		OrchEthAddress: addr,
		Version:        "test",
	}); err != nil {
		t.Fatal(err)
	}
	srv.UploadRoutes(rec)
	if _, err := srv.Listen(); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go srv.Serve(ctx)

	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	client := &http.Client{
		Jar:           jar,
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse },
	}
	loginResp, err := client.Post(
		"http://"+srv.Addr()+"/login",
		"application/x-www-form-urlencoded",
		strings.NewReader("admin_token=admin-token&actor=operator2"),
	)
	if err != nil {
		t.Fatal(err)
	}
	loginResp.Body.Close()
	if loginResp.StatusCode != http.StatusSeeOther {
		t.Fatalf("expected 303, got %d", loginResp.StatusCode)
	}

	body := signedBody(t, priv, addr, 1)
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	fw, err := w.CreateFormFile("manifest", "signed.json")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fw.Write(body); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	req, err := http.NewRequest(http.MethodPost, "http://"+srv.Addr()+"/admin/signed-manifest", &buf)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", w.FormDataContentType())
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	events, err := auditLog.Recent(5)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, event := range events {
		if event.Outcome != audit.OutcomeAccepted {
			continue
		}
		if event.Actor != "operator2" {
			t.Fatalf("actor = %q, want operator2", event.Actor)
		}
		if event.Uploader != "operator2" {
			t.Fatalf("uploader = %q, want operator2", event.Uploader)
		}
		found = true
	}
	if !found {
		t.Fatal("expected accepted audit event")
	}
}

func TestUpload_UIRouteRedirectsBackToRoster(t *testing.T) {
	rec, auditLog, priv, addr := newRecv(t)
	srv := New("127.0.0.1:0", slog.Default(), []string{"admin-token"})
	if err := srv.WebRoutes(WebDeps{
		Audit:          auditLog,
		Receive:        rec,
		OrchEthAddress: addr,
		Version:        "test",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := srv.Listen(); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go srv.Serve(ctx)

	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	client := &http.Client{
		Jar:           jar,
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse },
	}
	loginResp, err := client.Post(
		"http://"+srv.Addr()+"/login",
		"application/x-www-form-urlencoded",
		strings.NewReader("admin_token=admin-token&actor=operator3"),
	)
	if err != nil {
		t.Fatal(err)
	}
	loginResp.Body.Close()

	body := signedBody(t, priv, addr, 1)
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	fw, err := w.CreateFormFile("manifest", "signed.json")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fw.Write(body); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	req, err := http.NewRequest(http.MethodPost, "http://"+srv.Addr()+"/upload-signed-manifest", &buf)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", w.FormDataContentType())
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("expected 303, got %d", resp.StatusCode)
	}
	loc := resp.Header.Get("Location")
	if !strings.Contains(loc, "upload_outcome=accepted") {
		t.Fatalf("unexpected redirect %q", loc)
	}
}

func TestUpload_RosterRendersUploadFlash(t *testing.T) {
	rec, auditLog, _, addr := newRecv(t)
	srv := New("127.0.0.1:0", slog.Default(), []string{"admin-token"})
	if err := srv.WebRoutes(WebDeps{
		Audit:          auditLog,
		Receive:        rec,
		OrchEthAddress: addr,
		Version:        "test",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := srv.Listen(); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go srv.Serve(ctx)

	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	client := &http.Client{
		Jar:           jar,
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse },
	}
	loginResp, err := client.Post(
		"http://"+srv.Addr()+"/login",
		"application/x-www-form-urlencoded",
		strings.NewReader("admin_token=admin-token&actor=operator4"),
	)
	if err != nil {
		t.Fatal(err)
	}
	loginResp.Body.Close()

	resp, err := client.Get("http://" + srv.Addr() + "/?upload_message=candidate+drift&upload_outcome=drift_rejected")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	page := string(body)
	if !strings.Contains(page, "drift_rejected") {
		t.Fatalf("missing upload outcome in page: %s", page)
	}
	if !strings.Contains(page, "candidate drift") {
		t.Fatalf("missing upload message in page: %s", page)
	}
	if got := resp.Header.Get("Cache-Control"); !strings.Contains(got, "no-store") {
		t.Fatalf("cache-control %q, want no-store", got)
	}
}
