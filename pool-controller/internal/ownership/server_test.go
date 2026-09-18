package ownership

import (
	"context"
	"encoding/json"
	"encoding/pem"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	api "github.com/Cloud-SPE/livepeer-network-modules/pool-commons/ownership"
	"github.com/Cloud-SPE/livepeer-network-modules/pool-commons/serviceauth"
	"github.com/Cloud-SPE/livepeer-network-modules/pool-controller/internal/repo"
)

func TestHTTPSOwnershipRolesAndOutage(t *testing.T) {
	store, err := repo.OpenOwnership(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	dir := t.TempDir()
	authPath := filepath.Join(dir, "auth.json")
	tokenPath := filepath.Join(dir, "token")
	caPath := filepath.Join(dir, "ca.pem")
	token, hash, err := serviceauth.GenerateToken()
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(serviceauth.File{Credentials: []serviceauth.Credential{{ID: "eu-controller", PoolID: "eu", TokenSHA256: hash, Roles: []string{"ownership-controller"}, Resources: []string{"ownership"}, ExpiresAt: time.Now().Add(time.Hour)}}})
	if err := os.WriteFile(authPath, raw, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(tokenPath, []byte(token), 0600); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewTLSServer(Handler(store, serviceauth.Verifier{Path: authPath}))
	defer server.Close()
	if err := os.WriteFile(caPath, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: server.Certificate().Raw}), 0600); err != nil {
		t.Fatal(err)
	}
	cfg := api.Config{URL: server.URL, PoolID: "eu", TokenFile: tokenPath, CAFile: caPath}
	client, err := api.NewClient(cfg)
	if err != nil {
		t.Fatal(err)
	}
	claim, err := client.Claim(context.Background(), api.Request{DeviceID: "GPU-one", EnrollmentID: "host", MemberWallet: "wallet"})
	if err != nil {
		t.Fatal(err)
	}
	req := api.Request{DeviceID: claim.DeviceID, EnrollmentID: "host", ExpectedGeneration: claim.Generation, DestinationPoolID: "us", Reason: "source gone", StopEvidence: "operator fenced host", RevocationEvidence: "revoked"}
	if _, err := client.FencedRecovery(context.Background(), req); err == nil {
		t.Fatal("controller escalated to recovery admin")
	}
	cfg.PoolID = "us"
	wrong, err := api.NewClient(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := wrong.Claim(context.Background(), api.Request{DeviceID: "GPU-other", EnrollmentID: "host", MemberWallet: "wallet"}); err == nil {
		t.Fatal("wrong pool credential accepted")
	}
	server.Close()
	if _, err := client.Claim(context.Background(), api.Request{DeviceID: "GPU-other", EnrollmentID: "host", MemberWallet: "wallet"}); err == nil {
		t.Fatal("outage authorized new assignment")
	}
	durable, err := store.Get(claim.DeviceID)
	if err != nil || durable != claim {
		t.Fatal("outage changed existing assignment")
	}
}
