package poolcontroller

import (
	"context"
	"encoding/json"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Cloud-SPE/livepeer-network-modules/pool-payout-executor/internal/config"
)

func TestRegionalClaimChecksSourceAndReloadsCredential(t *testing.T) {
	intent := PayoutIntent{ID: "intent", PoolID: "pool_us", PayoutBatchID: "batch", Revision: 1}
	token := strings.Repeat("a", 64)
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+token {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		if r.Header.Get("X-Livepeer-Pool-ID") != "pool_us" {
			t.Error("missing pool scope")
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"lease_id": "lease", "intents": []PayoutIntent{intent}})
	}))
	defer server.Close()
	dir := t.TempDir()
	tokenPath := filepath.Join(dir, "token")
	caPath := filepath.Join(dir, "ca.pem")
	if err := os.WriteFile(tokenPath, []byte(token), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(caPath, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: server.Certificate().Raw}), 0600); err != nil {
		t.Fatal(err)
	}
	client, err := NewClient(config.PoolController{URL: server.URL, PoolID: "pool_us", TokenFile: tokenPath, CAFile: caPath})
	if err != nil {
		t.Fatal(err)
	}
	claim := func() error {
		_, _, err := client.ClaimPayoutIntents(context.Background(), ClaimPayoutIntentsRequest{ExecutorID: "regional", Limit: 1})
		return err
	}
	if err := claim(); err != nil {
		t.Fatal(err)
	}
	original := intent
	for _, bad := range []PayoutIntent{{ID: "intent", PoolID: "pool_eu", PayoutBatchID: "batch", Revision: 1}, {ID: "intent", PoolID: "pool_us", Revision: 1}, {ID: "intent", PoolID: "pool_us", PayoutBatchID: "batch"}, {ID: "legacy"}} {
		intent = bad
		if err := claim(); err == nil {
			t.Fatalf("accepted unqualified claim %+v", bad)
		}
	}
	intent = original
	token = strings.Repeat("b", 64)
	if err := claim(); err == nil {
		t.Fatal("revoked credential accepted")
	}
	if err := os.WriteFile(tokenPath, []byte(token), 0600); err != nil {
		t.Fatal(err)
	}
	if err := claim(); err != nil {
		t.Fatal(err)
	}
	legacy := &Client{}
	if err := legacy.validateRegionalIntents([]PayoutIntent{intent}); err == nil {
		t.Fatal("legacy executor accepted regional payout")
	}
}
