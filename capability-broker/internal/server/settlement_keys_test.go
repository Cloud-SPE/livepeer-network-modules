package server

import (
	"encoding/hex"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/ethereum/go-ethereum/crypto"

	"github.com/Cloud-SPE/livepeer-network-modules/capability-broker/internal/payment"
	"github.com/Cloud-SPE/livepeer-network-modules/capability-broker/internal/server/registry"
	"github.com/Cloud-SPE/livepeer-network-modules/capability-broker/internal/settlement"
)

func fetchSettlementKeys(t *testing.T, url string) registry.SettlementKeysPayload {
	t.Helper()
	resp, err := http.Get(url + "/registry/settlement-keys")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d", resp.StatusCode)
	}
	if cc := resp.Header.Get("Cache-Control"); cc != "no-store" {
		t.Fatalf("Cache-Control = %q, want no-store", cc)
	}
	var out registry.SettlementKeysPayload
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatal(err)
	}
	return out
}

// A delegating broker announces its key with a proof the coordinator
// can check; the announcement names the orch and this broker.
func TestRegistrySettlementKeys_AnnouncesProvenKey(t *testing.T) {
	key, err := crypto.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	keyFile := filepath.Join(t.TempDir(), "settlement.key")
	if err := os.WriteFile(keyFile, []byte(hex.EncodeToString(crypto.FromECDSA(key))), 0o600); err != nil {
		t.Fatal(err)
	}
	ts, _ := newJobOfferBrokerBare(t, payment.NewMock(), keyFile)

	got := fetchSettlementKeys(t, ts.URL)
	if got.OrchEthAddress == "" || got.SpecVersion == "" {
		t.Fatalf("payload header: %+v", got)
	}
	if len(got.Keys) != 1 {
		t.Fatalf("keys: want 1, got %d", len(got.Keys))
	}
	a := got.Keys[0]
	wantPub := "0x" + hex.EncodeToString(crypto.FromECDSAPub(&key.PublicKey))
	if a.Statement.PublicKey != wantPub {
		t.Fatalf("public_key = %s, want %s", a.Statement.PublicKey, wantPub)
	}
	if a.Statement.OrchEthAddress != got.OrchEthAddress {
		t.Fatalf("statement orch %s != payload orch %s", a.Statement.OrchEthAddress, got.OrchEthAddress)
	}
	if err := settlement.VerifyAnnouncement(a); err != nil {
		t.Fatalf("proof: %v", err)
	}
}

// No delegated key is a fact the endpoint states, not a failure.
func TestRegistrySettlementKeys_NoKeyIsEmptyList(t *testing.T) {
	ts, _ := newJobOfferBrokerBare(t, payment.NewMock(), "")
	got := fetchSettlementKeys(t, ts.URL)
	if got.Keys == nil || len(got.Keys) != 0 {
		t.Fatalf("keys = %#v, want empty list", got.Keys)
	}
}
