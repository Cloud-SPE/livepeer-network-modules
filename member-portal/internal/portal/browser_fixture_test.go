package portal

import (
	"archive/zip"
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Cloud-SPE/livepeer-network-modules/pool-commons/memberauth"
	"github.com/Cloud-SPE/livepeer-network-modules/pool-commons/memberreport"
	"github.com/ethereum/go-ethereum/accounts"
	"github.com/ethereum/go-ethereum/crypto"
)

// TestBrowserFixture runs only when explicitly requested by the local browser
// harness. Regional services are controlled fixtures; this is not live rollout.
func TestBrowserFixture(t *testing.T) {
	output := os.Getenv("PORTAL_BROWSER_FIXTURE")
	if output == "" {
		t.Skip("browser harness only")
	}
	dir := t.TempDir()
	walletKey, _ := crypto.GenerateKey()
	wallet := strings.ToLower(crypto.PubkeyToAddress(walletKey.PublicKey).Hex())
	public, private, _ := ed25519.GenerateKey(rand.Reader)
	now := time.Now()
	signerPath := filepath.Join(dir, "signer.json")
	trustPath := filepath.Join(dir, "trust.json")
	store, err := OpenStore(filepath.Join(dir, "portal.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	write := func(path string, v any) {
		raw, _ := json.Marshal(v)
		if err := os.WriteFile(path, raw, 0600); err != nil {
			t.Fatal(err)
		}
	}
	var appHandler atomic.Pointer[http.Handler]
	var stopOnce sync.Once
	stop := make(chan struct{})
	regionStates := map[string]*browserRegion{}
	portalServer := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		handler := appHandler.Load()
		if handler == nil {
			w.WriteHeader(503)
			return
		}
		switch r.URL.Path {
		case "/fixture/wallet":
			respond(w, 200, map[string]string{"wallet": wallet})
			return
		case "/fixture/sign":
			var input struct {
				Message string `json:"message"`
			}
			if json.NewDecoder(r.Body).Decode(&input) != nil {
				w.WriteHeader(400)
				return
			}
			message, err := hex.DecodeString(strings.TrimPrefix(input.Message, "0x"))
			if err != nil {
				w.WriteHeader(400)
				return
			}
			sig, err := crypto.Sign(accounts.TextHash(message), walletKey)
			if err != nil {
				t.Error(err)
				w.WriteHeader(500)
				return
			}
			respond(w, 200, map[string]string{"signature": "0x" + hex.EncodeToString(sig)})
			return
		case "/fixture/offline":
			region := regionStates[r.URL.Query().Get("pool")]
			if region == nil {
				w.WriteHeader(404)
				return
			}
			region.mu.Lock()
			region.offline = r.URL.Query().Get("value") == "true"
			region.mu.Unlock()
			w.WriteHeader(204)
			return
		case "/fixture/stop":
			stopOnce.Do(func() { close(stop) })
			w.WriteHeader(204)
			return
		}
		(*handler).ServeHTTP(w, r)
	}))
	defer portalServer.Close()
	write(signerPath, memberauth.PrivateKeyFile{Issuer: portalServer.URL, KeyID: "test", PrivateKey: base64.StdEncoding.EncodeToString(private)})
	write(trustPath, memberauth.Trust{Issuer: portalServer.URL, Keys: []memberauth.PublicKey{{ID: "test", PublicKey: base64.StdEncoding.EncodeToString(public), NotBefore: now.Add(-time.Hour), NotAfter: now.Add(time.Hour)}}})
	app := &Server{Store: store, Origin: portalServer.URL, Regions: map[string]*RegionalClient{}}
	for _, pool := range []string{"eu", "us"} {
		state := &browserRegion{pool: pool, wallet: wallet, trust: trustPath}
		regionStates[pool] = state
		regional := httptest.NewTLSServer(http.HandlerFunc(state.handle))
		defer regional.Close()
		ca := filepath.Join(dir, pool+".pem")
		os.WriteFile(ca, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: regional.Certificate().Raw}), 0600)
		client, err := NewRegionalClient(Region{PoolID: pool, Name: strings.ToUpper(pool), URL: regional.URL, CAFile: ca}, signerPath)
		if err != nil {
			t.Fatal(err)
		}
		app.Regions[pool] = client
	}
	handler, err := app.Handler()
	if err != nil {
		t.Fatal(err)
	}
	appHandler.Store(&handler)
	write(output, map[string]string{"origin": portalServer.URL, "wallet": wallet})
	select {
	case <-stop:
	case <-time.After(8 * time.Minute):
		t.Fatal("browser harness timeout")
	}
}

type browserRegion struct {
	mu                        sync.Mutex
	pool, wallet, trust       string
	joined, offline, enrolled bool
	retired                   bool
	transfer                  string
	optout                    bool
}

func (b *browserRegion) handle(w http.ResponseWriter, r *http.Request) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.offline {
		http.Error(w, "region unavailable", 503)
		return
	}
	if r.URL.Path == "/member/v1/public-region" {
		respond(w, 200, map[string]any{"pool_id": b.pool, "observed_at": time.Now(), "controller_status": "available", "offering_status": "configured; routing status belongs to brokers", "offerings": []map[string]string{{"template_id": "transcode", "name": "Transcode", "capability": "transcode", "offering_id": "video", "description": "Regional transcode"}}})
		return
	}
	if strings.HasSuffix(r.URL.Path, "/bundle") && r.Header.Get("Authorization") == "Bearer fixture-enrollment" {
		var buffer bytes.Buffer
		archive := zip.NewWriter(&buffer)
		f, _ := archive.Create("docker-compose.yaml")
		f.Write([]byte("services: {}\n"))
		archive.Close()
		w.Header().Set("Content-Type", "application/zip")
		w.Write(buffer.Bytes())
		return
	}
	claims, err := (memberauth.Verifier{Path: b.trust, PoolID: b.pool}).Verify(strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer "), time.Now())
	if err != nil || claims.Wallet != b.wallet {
		http.Error(w, "wrong member token", 401)
		return
	}
	switch {
	case r.URL.Path == "/member/v1/membership":
		acceptances := []map[string]any{}
		if b.joined {
			acceptances = append(acceptances, map[string]any{"terms_version": "v1", "accepted_at": time.Now()})
		}
		respond(w, 200, map[string]any{"pool_id": b.pool, "wallet": b.wallet, "joined": b.joined, "terms": []map[string]any{{"version": "v1", "effective_round": 100, "window_rounds": 14, "commission_bps": 1000, "participation_rules": "Regional participation rules"}}, "acceptances": acceptances})
	case r.URL.Path == "/member/v1/join":
		var req struct {
			PoolID  string `json:"pool_id"`
			Version string `json:"terms_version"`
		}
		json.NewDecoder(r.Body).Decode(&req)
		if req.PoolID != b.pool || req.Version != "v1" {
			w.WriteHeader(409)
			return
		}
		b.joined = true
		respond(w, 200, map[string]string{"pool_id": b.pool})
	case r.URL.Path == "/member/v1/enrollments" && r.Method == "GET":
		hosts := []map[string]any{}
		if b.enrolled {
			hosts = append(hosts, map[string]any{"id": "host-" + b.pool, "host_label": "GPU host", "status": func() string {
				if b.retired {
					return "retiring"
				}
				return "active"
			}()})
		}
		respond(w, 200, map[string]any{"enrollments": hosts})
	case r.URL.Path == "/member/v1/enrollments" || strings.HasSuffix(r.URL.Path, "/rotate"):
		if !b.joined {
			w.WriteHeader(403)
			return
		}
		b.enrolled = true
		respond(w, 200, map[string]any{"enrollment": map[string]string{"id": "host-" + b.pool, "pool_id": b.pool, "member_eth_address": b.wallet}, func() string {
			if strings.HasSuffix(r.URL.Path, "/rotate") {
				return "enrollment_token"
			}
			return "token"
		}(): "fixture-enrollment"})
	case strings.HasSuffix(r.URL.Path, "/retire"):
		b.retired = true
		respond(w, 200, map[string]string{"status": "retiring"})
	case strings.HasSuffix(r.URL.Path, "/status"):
		respond(w, 200, map[string]any{"last_seen_at": time.Now(), "gpus": []map[string]any{{"hardware_unit_id": "gpu-" + b.pool, "gpu_uuid": "gpu-fixture", "gpu_model": "GPU", "state": "active", "placements": []map[string]string{{"template_id": "transcode", "state": "probationary", "reason_code": "certification_passed", "evidence": "Passed readiness and paid-work probe"}}}}, "apply": map[string]any{"revision": "desired-1", "reported_at": time.Now(), "services": []map[string]string{{"name": "runner", "status": "running", "detail": "Container started"}}}})
	case strings.Contains(r.URL.Path, "/opt-outs"):
		if r.Method == "POST" {
			b.optout = true
		}
		if r.Method == "DELETE" {
			b.optout = false
		}
		items := []map[string]string{}
		if b.optout {
			items = append(items, map[string]string{"id": "optout-1", "template_id": "transcode", "reason": "Member preference"})
		}
		respond(w, 200, map[string]any{"opt_outs": items})
	case strings.HasSuffix(r.URL.Path, "/transfer"):
		b.transfer = "stopping"
		respond(w, 202, map[string]string{"phase": b.transfer})
	case r.URL.Path == "/member/v1/device-transfers":
		items := []map[string]any{}
		if b.transfer != "" {
			items = append(items, map[string]any{"id": "transfer-1", "device_id": "gpu-fixture", "destination_pool_id": "us", "phase": b.transfer, "last_error": "Waiting for actual agent stop confirmation", "updated_at": time.Now()})
		}
		respond(w, 200, map[string]any{"pool_id": b.pool, "transfers": items})
	case r.URL.Path == "/member/v1/regional-report":
		windows := []memberreport.Window{}
		if b.joined {
			windows = append(windows, memberreport.Window{PoolID: b.pool, WindowID: "window-100", BatchID: "batch-100", ChainID: 42161, Asset: "native_eth", StartRound: 100, EndRound: 113, TermsVersion: "v1", EarnedWei: "90", AwaitingApprovalWei: "0", PendingWei: "90", PaidWei: "0", Sources: []memberreport.SourceEvidence{{PoolID: b.pool, SourceID: "receiver-" + b.pool, BrokerID: "transcode", Round: 100, InclusionDigest: "confirmed-proof", WorkDigest: "work-proof"}}})
		}
		respond(w, 200, memberreport.Report{PoolID: b.pool, Wallet: b.wallet, ObservedAt: time.Now(), LedgerObservedAt: time.Now(), Windows: windows, CompleteRoundSpans: []memberreport.RoundSpan{{Start: 100, End: 113}}, Telemetry: memberreport.Telemetry{CompletedWindows: uint64(len(windows)), ConfirmedRevenueWei: "100", CommissionWei: "10", RoundingResidualWei: "0", ZeroWorkOperatorWei: "0"}})
	default:
		http.Error(w, "fixture member operation unsupported", 404)
	}
}
