package main

import (
	"bytes"
	"encoding/json"
	"encoding/pem"
	"github.com/Cloud-SPE/livepeer-network-modules/pool-commons/revenue"
	"github.com/Cloud-SPE/livepeer-network-modules/pool-controller/internal/types"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Cloud-SPE/livepeer-network-modules/pool-commons/serviceauth"
	"github.com/Cloud-SPE/livepeer-network-modules/pool-controller/internal/config"
	"github.com/Cloud-SPE/livepeer-network-modules/pool-controller/internal/repo"
)

func TestRegionalControllerServicePermissions(t *testing.T) {
	store, err := repo.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	token, hash, err := serviceauth.GenerateToken()
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "services.json")
	cred := serviceauth.Credential{ID: "caller", TokenSHA256: hash, PoolID: store.PoolID(), Roles: []string{"reconciler"}, Resources: []string{"controller"}, ExpiresAt: time.Now().Add(time.Hour)}
	save := func() {
		raw, _ := json.Marshal(serviceauth.File{Credentials: []serviceauth.Credential{cred}})
		if err := os.WriteFile(path, raw, 0600); err != nil {
			t.Fatal(err)
		}
	}
	save()
	state := &runtimeState{repo: store, cfg: &config.Config{ServiceAuthFile: path}, adminToken: "old-broad-token"}
	handler := withAdminAuth(state, func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200) })
	for _, tc := range []struct {
		method, path string
		status       int
	}{{"GET", "/admin/v1/work-receipts", 200}, {"POST", "/admin/v1/settlement-windows/close", 200}, {"POST", "/admin/v1/payout-batches/batch/approve", 401}, {"POST", "/admin/v1/payout-intents/status", 401}, {"POST", "/admin/v1/template-assignments", 401}} {
		req := httptest.NewRequest(tc.method, tc.path, nil)
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set(serviceauth.PoolHeader, store.PoolID())
		w := httptest.NewRecorder()
		handler(w, req)
		if w.Code != tc.status {
			t.Fatalf("%s %s returned %d", tc.method, tc.path, w.Code)
		}
	}
	cred.PoolID = "other-pool"
	save()
	req := httptest.NewRequest("GET", "/admin/v1/work-receipts", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set(serviceauth.PoolHeader, store.PoolID())
	w := httptest.NewRecorder()
	handler(w, req)
	if w.Code != 401 {
		t.Fatal("wrong pool accepted")
	}
	req.Header.Set("Authorization", "Bearer old-broad-token")
	w = httptest.NewRecorder()
	handler(w, req)
	if w.Code != 401 {
		t.Fatal("broad token bypassed scoped mode")
	}
}

func TestReceiptIngestionBindsCredentialSource(t *testing.T) {
	st, err := repo.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	token, hash, err := serviceauth.GenerateToken()
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "services.json")
	cred := serviceauth.Credential{ID: "broker", PoolID: st.PoolID(), SourceID: "source", Roles: []string{"broker"}, Resources: []string{"controller"}, ExpiresAt: time.Now().Add(time.Hour), TokenSHA256: hash}
	raw, _ := json.Marshal(serviceauth.File{Credentials: []serviceauth.Credential{cred}})
	if err = os.WriteFile(path, raw, 0600); err != nil {
		t.Fatal(err)
	}
	state := &runtimeState{repo: st, cfg: &config.Config{ServiceAuthFile: path}}
	mux := newAdminServeMux(state)
	receipt := types.WorkReceipt{ID: "source/operation", PoolID: st.PoolID(), SourceID: "source", RoundID: "100", CreatedAt: time.Now().UTC(), RequestID: "request", MemberEthAddress: "member", BackendID: "host|gpu", CapabilityID: "cap", OfferingID: "offer", ActualUnits: 42, AttributedRevenueWei: "42", Status: "final"}
	post := func(item types.WorkReceipt) int {
		raw, _ := json.Marshal(item)
		req := httptest.NewRequest("POST", "/admin/v1/work-receipts", bytes.NewReader(raw))
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set(serviceauth.PoolHeader, st.PoolID())
		out := httptest.NewRecorder()
		mux.ServeHTTP(out, req)
		return out.Code
	}
	for _, item := range []types.WorkReceipt{receipt, receipt} {
		if status := post(item); status != 200 {
			t.Fatalf("receipt ingest %d", status)
		}
	}
	changed := receipt
	changed.ID = "another/operation"
	changed.SourceID = "another"
	if status := post(changed); status != 403 {
		t.Fatalf("foreign source status=%d", status)
	}
	changed = receipt
	changed.AttributedRevenueWei = "43"
	if status := post(changed); status == 200 {
		t.Fatal("rewrite accepted")
	}
}

func TestSourceRetirementFetchesPinnedHTTPSProof(t *testing.T) {
	st, err := repo.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	now := time.Now().UTC()
	adminToken, adminDigest, err := serviceauth.GenerateToken()
	if err != nil {
		t.Fatal(err)
	}
	readToken, _, err := serviceauth.GenerateToken()
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	authPath := filepath.Join(dir, "auth.json")
	tokenPath := filepath.Join(dir, "reader")
	caPath := filepath.Join(dir, "ca.pem")
	auth, _ := json.Marshal(serviceauth.File{Credentials: []serviceauth.Credential{{ID: "operator", PoolID: st.PoolID(), Roles: []string{"pool-admin"}, Resources: []string{"controller"}, TokenSHA256: adminDigest, ExpiresAt: now.Add(time.Hour)}}})
	os.WriteFile(authPath, auth, 0600)
	os.WriteFile(tokenPath, []byte(readToken), 0600)
	source := revenue.Source{PoolID: st.PoolID(), SourceID: "0x" + strings.Repeat("a", 64), BrokerID: "broker", ChainID: 42161, Payee: "0x" + strings.Repeat("b", 40), TokenFile: tokenPath, CAFile: caPath}
	calls := 0
	broker := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.URL.Path != "/reporting/v1/source" || r.Header.Get("Authorization") != "Bearer "+readToken || r.Header.Get(serviceauth.PoolHeader) != source.PoolID {
			http.Error(w, "scope denied", 401)
			return
		}
		json.NewEncoder(w).Encode(revenue.DrainReport{PoolID: source.PoolID, SourceID: source.SourceID, BrokerID: source.BrokerID, ChainID: source.ChainID, Payee: source.Payee, Draining: true, Frozen: true, FrozenAt: now, Complete: true, CompleteThroughRound: 100, ObservedAt: now})
	}))
	defer broker.Close()
	source.URL = broker.URL
	os.WriteFile(caPath, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: broker.Certificate().Raw}), 0600)
	if err = st.RegisterRevenueSource(source, 100, "initial"); err != nil {
		t.Fatal(err)
	}
	empty, _ := revenue.WorkDigest(nil)
	page, _ := st.PageWorkReceipts("100", "final", "", "", 500)
	round := types.RoundReceipt{ID: "close-100", PoolID: st.PoolID(), RoundID: "100", PoolRevenueWei: "0", DistributableWei: "0", PoolCutWei: "0", ReceiptSnapshot: page.Snapshot, RevenueReports: []revenue.Report{{PoolID: source.PoolID, SourceID: source.SourceID, BrokerID: source.BrokerID, ChainID: source.ChainID, Payee: source.Payee, Round: 100, Complete: true, CompleteThroughRound: 100, ObservedAt: now, RevenueWei: "0", ObservedHead: 1000, FinalizedBlock: 996, ObservedHeadHash: "0x" + strings.Repeat("c", 64), FinalizedBlockHash: "0x" + strings.Repeat("d", 64), InclusionDigest: strings.Repeat("e", 64)}}, WorkReports: []revenue.WorkReport{{PoolID: source.PoolID, SourceID: source.SourceID, BrokerID: source.BrokerID, Round: 100, Complete: true, ClosedThroughRound: 100, ObservedAt: now, ReceiptDigest: empty}}}
	if err = st.SaveRegionalRound(round); err != nil {
		t.Fatal(err)
	}
	if err = st.DrainRevenueSource(source.SourceID, 100, "stop"); err != nil {
		t.Fatal(err)
	}
	state := &runtimeState{repo: st, cfg: &config.Config{ServiceAuthFile: authPath, RevenueSources: []revenue.Source{source}}}
	mux := newAdminServeMux(state)
	post := func(body string) int {
		req := httptest.NewRequest("POST", "/admin/v1/revenue-sources/"+source.SourceID+"/retire", strings.NewReader(body))
		req.Header.Set("Authorization", "Bearer "+adminToken)
		req.Header.Set(serviceauth.PoolHeader, st.PoolID())
		out := httptest.NewRecorder()
		mux.ServeHTTP(out, req)
		return out.Code
	}
	if code := post(`{"after_round":100,"reason":"retire","proof":{"complete":true}}`); code != 400 || calls != 0 {
		t.Fatalf("caller-supplied proof accepted code=%d calls=%d", code, calls)
	}
	if code := post(`{"after_round":100,"reason":"settled retirement"}`); code != 204 || calls != 1 {
		t.Fatalf("retirement code=%d calls=%d", code, calls)
	}
	if code := post(`{"after_round":100,"reason":"retry"}`); code != 204 || calls != 1 {
		t.Fatalf("idempotent retirement code=%d calls=%d", code, calls)
	}
	records, _ := st.RevenueSources(nil)
	if len(records) != 1 || records[0].RetirementProof == nil || records[0].State != "retired" {
		t.Fatalf("retirement evidence %+v", records)
	}
}
