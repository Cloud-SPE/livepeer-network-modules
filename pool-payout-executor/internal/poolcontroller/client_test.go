package poolcontroller

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Cloud-SPE/livepeer-network-modules/pool-payout-executor/internal/config"
)

func TestClientListAndUpdatePayoutIntents(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/admin/v1/payout-intents":
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"intents":[{"id":"payout-124-0xabc","round_id":"124","member_eth_address":"0xabc","amount_wei":"1800","status":"exported"}]}`)
		case r.Method == http.MethodPost && r.URL.Path == "/admin/v1/payout-intents/status":
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"intents":[{"id":"payout-124-0xabc","round_id":"124","member_eth_address":"0xabc","amount_wei":"1800","status":"submitted","external_ref":"batch-17","tx_hash":"0xabc123"}]}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	client, err := NewClient(config.PoolController{URL: server.URL, TimeoutMS: 500})
	if err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}
	intents, err := client.ListPayoutIntents(context.Background(), ListPayoutIntentsOptions{Status: "exported", Limit: 10})
	if err != nil {
		t.Fatalf("ListPayoutIntents() error = %v", err)
	}
	if len(intents) != 1 || intents[0].ID != "payout-124-0xabc" {
		t.Fatalf("ListPayoutIntents() = %+v", intents)
	}
	updated, err := client.UpdatePayoutIntentStatus(context.Background(), UpdatePayoutIntentStatusRequest{
		IDs:         []string{"payout-124-0xabc"},
		Status:      "submitted",
		ExternalRef: "batch-17",
		TxHash:      "0xabc123",
	})
	if err != nil {
		t.Fatalf("UpdatePayoutIntentStatus() error = %v", err)
	}
	if len(updated) != 1 || updated[0].ExternalRef != "batch-17" || updated[0].TxHash != "0xabc123" {
		t.Fatalf("UpdatePayoutIntentStatus() = %+v", updated)
	}
}
