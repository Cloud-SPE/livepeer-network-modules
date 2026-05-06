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

func newRecv(t *testing.T) (*receive.Service, *ecdsa.PrivateKey, string) {
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
	return receive.New(store, log, addr, candidate.SpecVersion), priv, addr
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
	rec, priv, addr := newRecv(t)
	srv := New("127.0.0.1:0", slog.Default())
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
	rec, _, _ := newRecv(t)
	srv := New("127.0.0.1:0", slog.Default())
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
