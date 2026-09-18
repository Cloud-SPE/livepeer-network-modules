package server

import (
	"context"
	"encoding/json"
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
	"github.com/Cloud-SPE/livepeer-network-modules/pool-commons/serviceauth"
)

type termsAccount struct{ payment.AccountClient }

func (*termsAccount) FreezeRevenueSource(context.Context, string) (*pb.RevenueSourceStatus, error) {
	panic("terms must not freeze receiver")
}
func (*termsAccount) RevenueSourceStatus(context.Context) (*pb.RevenueSourceStatus, error) {
	return &pb.RevenueSourceStatus{SettlementDomainId: "source"}, nil
}
func TestTermsPolicyScopeRotationAndQualifiedResponse(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "auth.json")
	token, digest, err := serviceauth.GenerateToken()
	if err != nil {
		t.Fatal(err)
	}
	credential := serviceauth.Credential{ID: "controller", PoolID: "pool", Roles: []string{"controller"}, Resources: []string{"broker"}, TokenSHA256: digest, ExpiresAt: time.Now().Add(time.Hour)}
	save := func() {
		raw, _ := json.Marshal(serviceauth.File{Credentials: []serviceauth.Credential{credential}})
		if err := os.WriteFile(path, raw, 0600); err != nil {
			t.Fatal(err)
		}
	}
	save()
	ledger, err := workledger.Open(filepath.Join(dir, "work.db"), "pool", "source", "broker")
	if err != nil {
		t.Fatal(err)
	}
	defer ledger.Close()
	if err := ledger.Finalize(99, time.Now()); err != nil {
		t.Fatal(err)
	}
	srv := &Server{cfg: &config.Config{PoolID: "pool", ServiceResource: "broker", ServiceAuthFile: path}, workAccounting: &workledger.Client{Store: ledger, AccountClient: &termsAccount{}, RequireTerms: true}}
	call := func(method, body, pool string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, "/admin/v1/terms-policy", strings.NewReader(body))
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set(serviceauth.PoolHeader, pool)
		out := httptest.NewRecorder()
		srv.handleTermsPolicy(out, req)
		return out
	}
	read := call("GET", "", "pool")
	if read.Code != 200 || !strings.Contains(read.Body.String(), `"source_id":"source"`) || !strings.Contains(read.Body.String(), `"broker_id":"broker"`) {
		t.Fatalf("identity %+v", read)
	}
	pause := `{"pool_id":"pool","action":"pause","expected_revision":0,"reason":"new terms"}`
	if out := call("POST", pause, "other"); out.Code != 401 {
		t.Fatal("cross-pool pause accepted")
	}
	credential.Roles = []string{"revenue-reader"}
	save()
	if out := call("POST", pause, "pool"); out.Code != 401 {
		t.Fatal("reader mutated terms policy")
	}
	credential.Roles = []string{"controller"}
	save()
	if out := call("POST", pause, "pool"); out.Code != 200 {
		t.Fatalf("pause %d %s", out.Code, out.Body.String())
	}
	if out := call("POST", `{"pool_id":"pool","action":"activate","expected_revision":1,"version":"v1","effective_round":100}`, "pool"); out.Code != 200 {
		t.Fatalf("activate %d %s", out.Code, out.Body.String())
	}
	credential.ExpiresAt = time.Now().Add(-time.Minute)
	save()
	if out := call("GET", "", "pool"); out.Code != 401 {
		t.Fatal("expired policy reader accepted")
	}
}
