package server

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Cloud-SPE/livepeer-network-modules/capability-broker/internal/config"
	"github.com/Cloud-SPE/livepeer-network-modules/capability-broker/internal/credentialstore"
	"github.com/Cloud-SPE/livepeer-network-modules/capability-broker/internal/payment"
	"github.com/Cloud-SPE/livepeer-network-modules/capability-broker/internal/runnerattach"
	"github.com/Cloud-SPE/livepeer-network-modules/capability-broker/internal/workledger"
	pb "github.com/Cloud-SPE/livepeer-network-modules/livepeer-network-protocol/proto-go/livepeer/payments/v1"
	"github.com/Cloud-SPE/livepeer-network-modules/pool-commons/ownership"
	"google.golang.org/protobuf/proto"
)

// The only synthetic payment boundary is the outstanding authorization state.
// All drain/fence/revocation/dispatch decisions below use production broker code.
type transferFixtureAccount struct {
	payment.AccountClient
	settled string
}

func (a transferFixtureAccount) GetSpendAuthorization(context.Context, []byte, string, string) (*payment.SpendAuthorizationStatus, error) {
	state := pb.SpendAuthorizationState_SPEND_AUTHORIZATION_ADMITTED
	if _, err := os.Stat(a.settled); err == nil {
		state = pb.SpendAuthorizationState_SPEND_AUTHORIZATION_SETTLED
	}
	return &payment.SpendAuthorizationStatus{State: int32(state)}, nil
}

func TestRegionalTransferBrokerProcess(t *testing.T) {
	path := os.Getenv("REGIONAL_E2E_TRANSFER_BROKER")
	if path == "" {
		t.Skip("explicit cross-component transfer fixture")
	}
	var c struct {
		Dir, Listen, PoolID, SourceID, BrokerID, HostID, Wallet, Token, AuthFile, Settled string
		Ownership                                                                         ownership.Config
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err = json.Unmarshal(raw, &c); err != nil {
		t.Fatal(err)
	}
	ledgerPath := filepath.Join(c.Dir, "work.db")
	_, statErr := os.Stat(ledgerPath)
	ledger, err := workledger.Open(ledgerPath, c.PoolID, c.SourceID, c.BrokerID)
	if err != nil {
		t.Fatal(err)
	}
	defer ledger.Close()
	creds, err := credentialstore.Open(filepath.Join(c.Dir, "credentials.db"), make([]byte, 32), credentialstore.Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer creds.Close()
	if os.IsNotExist(statErr) {
		auth, err := proto.Marshal(&pb.SpendAuthorization{Payload: &pb.SpendAuthorizationPayload{WholesaleAccountId: "test-account", AuthorizationId: "active-transfer-work", SettlementDomainId: c.SourceID, Payer: make([]byte, 20)}})
		if err != nil {
			t.Fatal(err)
		}
		if err = ledger.SaveAuthorization("active-transfer-work", auth); err != nil {
			t.Fatal(err)
		}
		if err = ledger.Bind("active-transfer-work", workledger.Attribution{Member: c.Wallet, Enrollment: c.HostID, Backend: c.HostID + "|gpu-a", Capability: "test:work", Offering: "test", DeviceOwnership: map[string]uint64{"gpu-a": 1}}); err != nil {
			t.Fatal(err)
		}
		_, err = creds.SyncReplace("initial", []credentialstore.SyncEntry{{CredentialID: "regional-transfer", HostID: c.HostID, PoolID: c.PoolID, TokenSHA256: credentialstore.HashToken(c.Token), ExpiresAt: time.Now().Add(time.Hour), DeviceOwnership: map[string]uint64{"gpu-a": 1, "gpu-b": 1}}})
		if err != nil {
			t.Fatal(err)
		}
	}
	srv := &Server{cfg: &config.Config{PoolID: c.PoolID, ServiceResource: c.BrokerID, ServiceAuthFile: c.AuthFile, Ownership: c.Ownership}, credentialStore: creds, ownershipChecks: map[ownershipCacheKey]uint64{}, workAccounting: &workledger.Client{Store: ledger, AccountClient: transferFixtureAccount{settled: c.Settled}}}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /admin/v1/devices/drain", srv.handleDeviceDrain)
	mux.HandleFunc("GET /test/dispatch/{device}", func(w http.ResponseWriter, r *http.Request) {
		doc := &runnerattach.Document{HostID: c.HostID, Capabilities: []runnerattach.Capability{{Devices: []string{r.PathValue("device")}}}}
		result := &runnerattach.Result{Capabilities: []runnerattach.CapabilityResult{{Index: 0, Status: "accepted"}}}
		srv.checkAttachOwnership(doc, result)
		if result.Capabilities[0].Status != "accepted" || !srv.authorizeDeviceDispatch(c.HostID, doc.Capabilities[0]) {
			http.Error(w, "fenced", 409)
			return
		}
		w.WriteHeader(204)
	})
	mux.HandleFunc("GET /test/work", func(w http.ResponseWriter, r *http.Request) {
		if !srv.authorizeDeviceDispatch(c.HostID, runnerattach.Capability{Devices: []string{"gpu-a"}}) {
			http.Error(w, "fenced", 409)
			return
		}
		if err := os.WriteFile(c.Settled+".inflight", []byte("synthetic work request admitted"), 0600); err != nil {
			http.Error(w, "fixture unavailable", 500)
			return
		}
		ticker := time.NewTicker(25 * time.Millisecond)
		defer ticker.Stop()
		for {
			if _, err := os.Stat(c.Settled); err == nil {
				w.WriteHeader(204)
				return
			}
			select {
			case <-r.Context().Done():
				return
			case <-ticker.C:
			}
		}
	})
	mux.HandleFunc("GET /test/health", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(204) })
	// Loopback-only fixture listener, behind the e2e TLS proxy for scoped APIs.
	if err = http.ListenAndServe(c.Listen, mux); err != nil {
		t.Fatal(err)
	}
}
