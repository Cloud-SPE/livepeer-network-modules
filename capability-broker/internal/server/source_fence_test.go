package server

import (
	"context"
	"encoding/json"
	"fmt"
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
	"github.com/Cloud-SPE/livepeer-network-modules/pool-commons/serviceauth"
)

type fenceReceiver struct {
	payment.RevenueReader
	state *pb.RevenueSourceStatus
	calls int
}

func (f *fenceReceiver) FreezeRevenueSource(context.Context, string) (*pb.RevenueSourceStatus, error) {
	f.calls++
	if f.state.ActiveAuthorizations != 0 {
		return nil, fmt.Errorf("active work remains")
	}
	f.state.Frozen = true
	return f.state, nil
}
func (f *fenceReceiver) RevenueSourceStatus(context.Context) (*pb.RevenueSourceStatus, error) {
	return f.state, nil
}
func TestSourceFreezeRequiresDrainAndScopedMutationRole(t *testing.T) {
	dir := t.TempDir()
	auth := filepath.Join(dir, "auth.json")
	token, digest, err := serviceauth.GenerateToken()
	if err != nil {
		t.Fatal(err)
	}
	cred := serviceauth.Credential{ID: "controller", PoolID: "pool", Roles: []string{"controller"}, Resources: []string{"broker"}, TokenSHA256: digest, ExpiresAt: time.Now().Add(time.Hour)}
	save := func() {
		raw, _ := json.Marshal(serviceauth.File{Credentials: []serviceauth.Credential{cred}})
		if err := os.WriteFile(auth, raw, 0600); err != nil {
			t.Fatal(err)
		}
	}
	save()
	ledger, err := workledger.Open(filepath.Join(dir, "work.db"), "pool", "source", "broker")
	if err != nil {
		t.Fatal(err)
	}
	defer ledger.Close()
	receiver := &fenceReceiver{state: &pb.RevenueSourceStatus{SettlementDomainId: "source", ActiveAuthorizations: 1}}
	srv := &Server{cfg: &config.Config{PoolID: "pool", ServiceResource: "broker", ServiceAuthFile: auth}, workAccounting: &workledger.Client{Store: ledger, Reader: receiver}}
	call := func(path string, handler http.HandlerFunc) int {
		req := httptest.NewRequest("POST", path, strings.NewReader(`{"reason":"retirement"}`))
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set(serviceauth.PoolHeader, "pool")
		out := httptest.NewRecorder()
		handler(out, req)
		return out.Code
	}
	if code := call("/admin/v1/source/freeze", srv.handleSourceFreeze); code != 409 || receiver.calls != 0 {
		t.Fatal("freeze before drain accepted")
	}
	if code := call("/admin/v1/source/drain", srv.handleSourceDrain); code != 204 {
		t.Fatal(code)
	}
	if code := call("/admin/v1/source/freeze", srv.handleSourceFreeze); code != 409 {
		t.Fatal("active source frozen")
	}
	receiver.state.ActiveAuthorizations = 0
	if code := call("/admin/v1/source/freeze", srv.handleSourceFreeze); code != 204 || !receiver.state.Frozen {
		t.Fatal("settled source did not freeze")
	}
	before := receiver.calls
	cred.Roles = []string{"revenue-reader"}
	save()
	if code := call("/admin/v1/source/freeze", srv.handleSourceFreeze); code != 401 || receiver.calls != before {
		t.Fatal("read-only caller froze source")
	}
}
