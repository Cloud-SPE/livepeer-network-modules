package server

import (
	"context"
	"encoding/json"
	"encoding/pem"
	"math/big"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Cloud-SPE/livepeer-network-modules/capability-broker/internal/config"
	"github.com/Cloud-SPE/livepeer-network-modules/capability-broker/internal/payment"
	"github.com/Cloud-SPE/livepeer-network-modules/capability-broker/internal/workledger"
	pb "github.com/Cloud-SPE/livepeer-network-modules/livepeer-network-protocol/proto-go/livepeer/payments/v1"
	"github.com/Cloud-SPE/livepeer-network-modules/pool-commons/revenue"
	"github.com/Cloud-SPE/livepeer-network-modules/pool-commons/serviceauth"
)

type reportPayment struct {
	payment.Client
	report *pb.GetRoundRevenueResponse
	calls  int
}

func (p *reportPayment) RoundRevenue(context.Context, int64) (*pb.GetRoundRevenueResponse, error) {
	p.calls++
	return p.report, nil
}

func TestRegionalRevenueHTTPSAndLeastPrivilege(t *testing.T) {
	dir := t.TempDir()
	authFile := filepath.Join(dir, "auth.json")
	tokenFile := filepath.Join(dir, "token")
	caFile := filepath.Join(dir, "ca.pem")
	token, hash, err := serviceauth.GenerateToken()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(tokenFile, []byte(token), 0600); err != nil {
		t.Fatal(err)
	}
	cred := serviceauth.Credential{ID: "reader", PoolID: "pool_us", Roles: []string{"revenue-reader"}, Resources: []string{"audio-broker"}, ExpiresAt: time.Now().Add(time.Hour), TokenSHA256: hash}
	save := func() {
		raw, _ := json.Marshal(serviceauth.File{Credentials: []serviceauth.Credential{cred}})
		if err := os.WriteFile(authFile, raw, 0600); err != nil {
			t.Fatal(err)
		}
	}
	save()
	ledger := "0x" + strings.Repeat("a", 64)
	payer := []byte(strings.Repeat("b", 20))
	blockHash := []byte(strings.Repeat("c", 32))
	pay := &reportPayment{report: &pb.GetRoundRevenueResponse{RoundId: 100, SettlementDomainId: ledger, ChainId: 42161, Payee: payer, Complete: true, ConfirmedRevenueWei: big.NewInt(123).Bytes(), ConfirmedTicketCount: 601, ObservedHead: 1000, ObservedHeadHash: blockHash, FinalizedBlock: 996, FinalizedBlockHash: blockHash, CompleteThroughRound: 100, ObservedAt: time.Now().Format(time.RFC3339Nano), InclusionDigest: blockHash}}
	srv := &Server{cfg: &config.Config{PoolID: "pool_us", ServiceResource: "audio-broker", ServiceAuthFile: authFile}, payment: pay}
	work, err := workledger.Open(filepath.Join(dir, "work.db"), "pool_us", ledger, "audio-broker")
	if err != nil {
		t.Fatal(err)
	}
	defer work.Close()
	if err = work.Finalize(101, time.Now()); err != nil {
		t.Fatal(err)
	}
	srv.workAccounting = &workledger.Client{Store: work}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /reporting/v1/revenue/{round}", srv.handleRegionalRevenue)
	mux.HandleFunc("GET /reporting/v1/work/{round}", srv.handleRegionalWork)
	tls := httptest.NewTLSServer(mux)
	defer tls.Close()
	if err := os.WriteFile(caFile, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: tls.Certificate().Raw}), 0600); err != nil {
		t.Fatal(err)
	}
	source := revenue.Source{PoolID: "pool_us", SourceID: ledger, BrokerID: "audio-broker", ChainID: 42161, Payee: "0x" + strings.Repeat("62", 20), URL: tls.URL, TokenFile: tokenFile, CAFile: caFile}
	result, err := revenue.Collect(context.Background(), source, 100)
	if err != nil || result.RevenueWei != "123" || result.TicketCount != 601 {
		t.Fatalf("HTTPS report %+v %v", result, err)
	}
	if result, err := revenue.CollectWork(context.Background(), source, 100); err != nil || !result.Complete || result.ReceiptCount != 0 {
		t.Fatalf("complete zero work %+v %v", result, err)
	}
	req := httptest.NewRequest("PUT", "/admin/v1/offers", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set(serviceauth.PoolHeader, "pool_us")
	if srv.requireAdminAuth(httptest.NewRecorder(), req) {
		t.Fatal("revenue reader gained broker mutation")
	}
	for _, mutate := range []func(){func() { cred.PoolID = "pool_eu" }, func() { cred.PoolID = "pool_us"; cred.Roles = []string{"coordinator"} }, func() { cred.Roles = []string{"revenue-reader"}; cred.Revoked = true }} {
		before := pay.calls
		mutate()
		save()
		if _, err := revenue.Collect(context.Background(), source, 100); err == nil {
			t.Fatal("invalid scope accepted")
		}
		if _, err := revenue.CollectWork(context.Background(), source, 100); err == nil {
			t.Fatal("invalid work report scope accepted")
		}
		if pay.calls != before {
			t.Fatal("unauthorized caller reached receiver")
		}
	}
}
